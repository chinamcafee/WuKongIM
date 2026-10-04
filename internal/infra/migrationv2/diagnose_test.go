package migrationv2_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func diagnosticPlan(t *testing.T, source string) migration.Plan {
	return migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "diagnostic-fixture", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57881", DataDir: filepath.Join(t.TempDir(), "target")}}}}
}

func TestDiagnoseCollectsOriginalMessageFieldsAndSealsNoWorkflow(t *testing.T) {
	ctx := context.Background()
	plan := diagnosticPlan(t, unpackNamedFixture(t, "original-v2-server.tar.gz"))
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest()+":diagnose-v2", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	var details bytes.Buffer
	r := migrationv2.Reader{}
	report, err := migration.DiagnoseSources(ctx, plan, w, r, r, &details, nil)
	require.NoError(t, err)
	require.True(t, report.ScanComplete)
	require.Equal(t, "blocked", report.Status)
	require.False(t, report.CutoverReady)
	counts := map[string]uint64{}
	for _, c := range report.Categories {
		counts[c.Code] = c.Count
		require.LessOrEqual(t, len(c.Samples), 5)
	}
	require.Zero(t, counts["message.field.red_dot"], "RedDot is preserved by the target runtime")
	require.Positive(t, counts["message.field.sync_once"])
	require.Zero(t, counts["identity.hint_collision"])
	require.Zero(t, counts["Message.unresolved_identity"])
	require.Equal(t, uint64(4), report.Nodes[0].Tables["Message"])
	require.NotContains(t, details.String(), "消息0")
	sum := sha256.Sum256(details.Bytes())
	require.Equal(t, hex.EncodeToString(sum[:]), report.FindingsSHA256)
	for _, seal := range []string{"workflow/PREPARED", "conversion/COMPLETE"} {
		_, found, err := w.Get(ctx, []byte(seal))
		require.NoError(t, err)
		require.False(t, found)
	}
	var again bytes.Buffer
	repeated, err := migration.DiagnoseSources(ctx, plan, w, r, r, &again, nil)
	require.NoError(t, err)
	require.Equal(t, report, repeated)
	require.Equal(t, details.String(), again.String())
}

func TestDiagnoseCollectsDuplicateIDsAndAllIndexConflicts(t *testing.T) {
	source := compatibleMessageFixture(t)
	// Change only a private test copy: two group messages now share the first
	// ID while the original index still points to the first physical sequence.
	changed := rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) == 22 && binary.BigEndian.Uint16(key) == 0x0101 && key[2] == 1 && binary.BigEndian.Uint16(key[20:]) == 0x0104 && binary.BigEndian.Uint64(key[12:20]) == 2 {
			out := make([]byte, 8)
			binary.BigEndian.PutUint64(out, 2096462572973723648)
			require.NoError(t, b.Set(key, out, nil))
			return true
		}
		return false
	})
	require.Positive(t, changed)
	plan := diagnosticPlan(t, source)
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "duplicate-diagnostic", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	var details bytes.Buffer
	r := migrationv2.Reader{}
	report, err := migration.DiagnoseSources(context.Background(), plan, w, r, r, &details, nil)
	require.NoError(t, err)
	require.True(t, report.ScanComplete)
	counts := map[string]uint64{}
	for _, c := range report.Categories {
		counts[c.Code] = c.Count
	}
	require.Equal(t, uint64(1), counts["duplicate.message_id"])
	require.Equal(t, uint64(1), counts["index.expected_collision"])
	require.Positive(t, counts["index.value_mismatch"])
}

func TestDiagnoseCLIEmitsBlockedJSONAndCannotBeUsedAsPrepareWorkspace(t *testing.T) {
	plan := diagnosticPlan(t, unpackNamedFixture(t, "original-v2-server.tar.gz"))
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	data, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	var output, diagnostics bytes.Buffer
	workspace := filepath.Join(dir, "diagnostic")
	args := []string{"diagnose", "--plan", planPath, "--workspace", workspace}
	require.Equal(t, 1, migrationapp.Run(context.Background(), args, &output, &diagnostics), diagnostics.String())
	var report migration.DiagnosticReport
	require.NoError(t, json.Unmarshal(output.Bytes(), &report))
	require.True(t, report.ScanComplete)
	contents, err := os.ReadFile(report.FindingsFile)
	require.NoError(t, err)
	sum := sha256.Sum256(contents)
	require.Equal(t, hex.EncodeToString(sum[:]), report.FindingsSHA256)
	_, err = os.Stat(plan.Target.Nodes[0].DataDir)
	require.True(t, os.IsNotExist(err))
	output.Reset()
	diagnostics.Reset()
	args[0] = "prepare"
	require.Equal(t, 1, migrationapp.Run(context.Background(), args, &output, &diagnostics))
	require.Contains(t, diagnostics.String(), "identity")
}

func TestDiagnoseContinuesAfterUnreadableNodeAndReportsIncomplete(t *testing.T) {
	plan := diagnosticPlan(t, compatibleMessageFixture(t))
	plan.Sources = append([]migration.NodeOptions{{NodeID: 99, Options: migration.Options{DataDir: filepath.Join(t.TempDir(), "absent"), ShardCount: 2}}}, plan.Sources...)
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "partial-diagnostic", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	var details bytes.Buffer
	r := migrationv2.Reader{}
	report, err := migration.DiagnoseSources(context.Background(), plan, w, r, r, &details, nil)
	require.NoError(t, err)
	require.Equal(t, "incomplete", report.Status)
	require.False(t, report.ScanComplete)
	require.False(t, report.Nodes[0].Complete)
	require.True(t, report.Nodes[1].Complete)
	require.Equal(t, uint64(4), report.Nodes[1].Tables["Message"])
}

func TestDiagnoseLegacyExclusionKeepsAndChecksEveryMainMessage(t *testing.T) {
	source, _ := legacyStreamFixture(t)
	before := fileDigests(t, source)
	plan := diagnosticPlan(t, source)
	plan.Exclusions = &migration.Exclusions{LegacyStreamStorage: true}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "legacy-diagnostic", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	var details bytes.Buffer
	r := migrationv2.Reader{}
	report, err := migration.DiagnoseSources(context.Background(), plan, w, r, r, &details, nil)
	require.NoError(t, err)
	counts := map[string]uint64{}
	for _, c := range report.Categories {
		counts[c.Code] = c.Count
	}
	require.Equal(t, uint64(2), counts["legacy.stream_storage_excluded"])
	require.Positive(t, counts["message.field.stream_no"])
	require.Equal(t, uint64(4), report.Nodes[0].Tables["Message"])
	require.Equal(t, before, fileDigests(t, source))
}

func TestDiagnoseChecksSubscriberVisibilityWithMissingConversation(t *testing.T) {
	source := compatibleMessageFixture(t)
	require.Positive(t, rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) >= 4 && binary.BigEndian.Uint16(key) == 0x0901 {
			require.NoError(t, b.Delete(key, nil))
			return true
		}
		return false
	}))
	plan := diagnosticPlan(t, source)
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "visibility-diagnostic", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	var details bytes.Buffer
	r := migrationv2.Reader{}
	report, err := migration.DiagnoseSources(context.Background(), plan, w, r, r, &details, nil)
	require.NoError(t, err)
	var count uint64
	for _, c := range report.Categories {
		if c.Code == "conversation.visibility" {
			count = c.Count
		}
	}
	require.Positive(t, count)
}

func TestDiagnoseContinuesAcrossMisplacedDuplicateMessageHistories(t *testing.T) {
	source := compatibleMessageFixture(t)
	type kv struct{ key, value []byte }
	var copies []kv
	rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) >= 4 && binary.BigEndian.Uint16(key) == 0x0101 && key[2] != 2 && key[2] != 3 {
			copies = append(copies, kv{bytes.Clone(key), bytes.Clone(value)})
		}
		return false
	})
	for shard := 0; shard < 2; shard++ {
		db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", fmt.Sprintf("shard%03d", shard)), &pebble.Options{})
		require.NoError(t, err)
		b := db.NewBatch()
		for _, row := range copies {
			require.NoError(t, b.Set(row.key, row.value, nil))
		}
		require.NoError(t, b.Commit(pebble.Sync))
		require.NoError(t, b.Close())
		require.NoError(t, db.Close())
	}
	plan := diagnosticPlan(t, source)
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "misplaced-diagnostic", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	var details bytes.Buffer
	r := migrationv2.Reader{}
	report, err := migration.DiagnoseSources(context.Background(), plan, w, r, r, &details, nil)
	require.NoError(t, err)
	require.True(t, report.ScanComplete)
	counts := map[string]uint64{}
	for _, c := range report.Categories {
		counts[c.Code] = c.Count
	}
	require.Positive(t, counts["Message.index_shape_or_placement"])
	require.Equal(t, uint64(4), counts["duplicate.message_id"])
	require.Equal(t, uint64(8), report.Nodes[0].Tables["Message"])
}
