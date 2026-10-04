package migrationv2_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	"os"
	"path/filepath"
	"testing"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// duplicateConversionFixture mutates a private synthetic copy only: original
// seq 1 and 2 share both keys but have different payloads, so seq 2 must win.
func duplicateConversionFixture(t *testing.T) string {
	t.Helper()
	source := compatibleMessageFixture(t)
	var first, second migration.Row
	require.NoError(t, migrationv2.Scan(context.Background(), migration.Options{DataDir: source, ShardCount: 2}, func(r migration.Row) error {
		if r.Table == "Message" && r.Kind == migration.Primary && string(r.Fields["ChannelId"]) == "migrationgroup" {
			if r.ID == 1 {
				first = r
			}
			if r.ID == 2 {
				second = r
			}
		}
		return nil
	}))
	require.NotEmpty(t, first.Key)
	require.NotEmpty(t, second.Key)
	rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) == 14 && binary.BigEndian.Uint16(key) == 0x0101 && key[2] == byte(migration.Index) && bytes.Equal(key[6:], first.Fields["MessageId"]) {
			require.NoError(t, b.Delete(key, nil))
			return true
		}
		if len(key) == 22 && bytes.Equal(key[:20], first.Key) {
			switch binary.BigEndian.Uint16(key[20:]) {
			case 0x0104:
				require.NoError(t, b.Set(key, second.Fields["MessageId"], nil))
				return true
			case 0x0106:
				require.NoError(t, b.Set(key, second.Fields["ClientMsgNo"], nil))
				return true
			}
		}
		// Replace the exact live client lookup for seq 1; keep all sender indexes.
		if len(key) == 30 && binary.BigEndian.Uint16(key) == 0x0101 && key[2] == byte(migration.SecondaryIndex) && binary.BigEndian.Uint16(key[4:]) == 0x0102 && binary.BigEndian.Uint64(key[14:]) == first.Owner {
			if binary.BigEndian.Uint64(key[22:]) == 1 {
				require.NoError(t, b.Delete(key, nil))
				return true
			}
			if binary.BigEndian.Uint64(key[22:]) == 2 {
				k := bytes.Clone(key)
				binary.BigEndian.PutUint64(k[22:], 1)
				require.NoError(t, b.Set(k, value, nil))
				return true
			}
		}
		return false
	})
	return source
}

func TestMessagePolicyConvertsLatestAndOmitsCMDWithIndependentNativeVerification(t *testing.T) {
	for _, nodes := range []int{1, 3} {
		t.Run(fmt.Sprintf("single_node_source_to_%d_node_cluster", nodes), func(t *testing.T) { testMessagePolicyNativeCluster(t, nodes) })
	}
}
func testMessagePolicyNativeCluster(t *testing.T, nodes int) {
	ctx := context.Background()
	source := duplicateConversionFixture(t)
	addConversationCopy(t, source, 1, "")
	before := fileDigests(t, source)
	plan := diagnosticPlan(t, source)
	plan.Metadata = conversationPolicy()
	plan.Messages = &migration.MessagePolicy{KeepLatestDuplicates: true, ExcludeCMD: true, CompactSequences: true}
	if nodes == 3 {
		plan.Target.Replicas = 3
		plan.Target.ChannelReplicas = 3
		for i := 2; i <= 3; i++ {
			plan.Target.Nodes = append(plan.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + i), Addr: fmt.Sprintf("127.0.0.1:%d", 57880+i), DataDir: filepath.Join(t.TempDir(), "target")})
		}
	}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "scratch"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	prepared, err := migration.Prepare(ctx, plan, w, r, r, nil)
	require.NoError(t, err)
	tr := prepared.Conversion.Transformation
	require.NotNil(t, tr)
	require.EqualValues(t, 4, tr.Original)
	require.EqualValues(t, 2, tr.Retained)
	require.EqualValues(t, 1, tr.DuplicateDrops)
	require.EqualValues(t, 1, tr.CMDDrops)
	require.Positive(t, tr.CMDConversations)
	require.EqualValues(t, 2, prepared.Conversion.Messages)
	require.EqualValues(t, 1, prepared.Conversion.MessageChannels)
	var messages []channelcompat.Message
	require.NoError(t, migration.WalkTargetMessages(ctx, w, migration.ChannelIdentity{ID: "migrationgroup", Type: 2}, func(m channelcompat.Message) error { messages = append(messages, m); return nil }))
	require.Len(t, messages, 2)
	require.EqualValues(t, 1, messages[0].MessageSeq)
	require.Equal(t, []byte("消息1"), messages[0].Payload)
	require.EqualValues(t, 2096462572977917952, messages[0].MessageID)
	require.EqualValues(t, 2, messages[1].MessageSeq)
	require.Equal(t, []byte("消息2"), messages[1].Payload)
	var mappings []migration.MessageSequenceMapping
	require.NoError(t, migration.WalkMessageSequenceMappings(ctx, w, func(m migration.MessageSequenceMapping) error { mappings = append(mappings, m); return nil }))
	require.Len(t, mappings, 4)
	for _, m := range mappings {
		if m.Channel.ID == "migrationgroup" && m.OriginalSeq == 1 {
			require.Equal(t, "duplicate", m.Omitted)
			require.Zero(t, m.TargetSeq)
			require.Zero(t, m.BoundarySeq, "a read of old seq 1 must not mark the later winner read")
		}
	}
	require.NoError(t, migration.WalkTargetMetadata(ctx, w, func(r migration.TargetRecord) error {
		require.NotEqual(t, "cmd_membership", r.Table)
		if r.Table == "membership" {
			var m meta.UserChannelMembership
			require.NoError(t, migration.UnmarshalState(r.Value, &m))
			if m.UID == "migrationbob" {
				require.EqualValues(t, 2, m.ReadSeq)
				require.EqualValues(t, 2, m.DeletedToSeq)
			}
		}
		return nil
	}))
	again, err := migration.Prepare(ctx, plan, w, r, r, nil)
	require.NoError(t, err)
	require.Equal(t, prepared, again)
	require.NoError(t, migrationv3.Install(ctx, plan.Target, prepared.Conversion, w))
	verified, err := migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, tr, verified.Transformation)
	require.EqualValues(t, 2*nodes, verified.Messages)
	require.False(t, verified.CutoverReady)
	// Rebuild from the original archive in a fresh workspace, including policy.
	archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
	require.NoError(t, err)
	_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
	require.NoError(t, err)
	fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "rebuild"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	defer fresh.Close()
	rebuilt, err := migration.PrepareArchive(ctx, plan, fresh, r, archive)
	require.NoError(t, err)
	require.Equal(t, prepared.Conversion, rebuilt.Conversion)
	_, err = migration.VerifyTargets(ctx, plan.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, before, fileDigests(t, source))
	_, err = migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, r, wrongCursorInspector{})
	require.ErrorContains(t, err, "user_channel_membership")
	// Wrong cursor mapping leaves message rows and counts unchanged, but must fail.
	db, err := meta.Open(filepath.Join(plan.Target.Nodes[0].DataDir, "slotmeta"))
	require.NoError(t, err)
	for slot := uint16(0); slot < 256; slot++ {
		s := db.MetaDB().HashSlot(slot)
		m, found, err := s.GetUserChannelMembership(ctx, "migrationbob", "migrationgroup", 2)
		require.NoError(t, err)
		if found {
			m.ReadSeq = 3
			require.NoError(t, s.UpsertUserChannelMembership(ctx, m))
		}
	}
	require.NoError(t, db.Close())
	_, err = migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, r, migrationv3.Inspector{})
	require.ErrorContains(t, err, "completed target data changed")
}

func TestMessagePolicyDoesNotAcceptOldIndexPointingAtOlderDuplicate(t *testing.T) {
	source := duplicateConversionFixture(t)
	rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) == 14 && binary.BigEndian.Uint16(key) == 0x0101 && key[2] == byte(migration.Index) && binary.BigEndian.Uint64(key[6:]) == 2096462572977917952 {
			v := bytes.Clone(value)
			binary.BigEndian.PutUint64(v[8:], 1)
			require.NoError(t, b.Set(key, v, nil))
			return true
		}
		return false
	})
	plan := diagnosticPlan(t, source)
	plan.Messages = &migration.MessagePolicy{KeepLatestDuplicates: true, ExcludeCMD: true, CompactSequences: true}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "scratch"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	_, err = migration.Prepare(context.Background(), plan, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
	require.ErrorContains(t, err, "index points to a different primary")
}

// The read facade supplies a wrong cursor without altering files, so this
// specifically tests semantic verification in addition to the file hash guard.
type wrongCursorInspector struct{}

func (wrongCursorInspector) Open(ctx context.Context, p migration.TargetPlan, n migration.TargetNode) (migration.TargetView, error) {
	v, err := (migrationv3.Inspector{}).Open(ctx, p, n)
	if err != nil {
		return nil, err
	}
	return wrongCursorView{v}, nil
}

type wrongCursorView struct{ migration.TargetView }

func (v wrongCursorView) Metadata(ctx context.Context, table, owner string, key map[string]any) (map[string]any, bool, error) {
	got, found, err := v.TargetView.Metadata(ctx, table, owner, key)
	if err == nil && found && table == "user_channel_membership" && owner == "migrationbob" {
		got["read_seq"] = uint64(3)
	}
	return got, found, err
}

func TestMessagePolicyCLIEmitsMappingAndCMDPlanning(t *testing.T) {
	source := duplicateConversionFixture(t)
	plan := diagnosticPlan(t, source)
	plan.Messages = &migration.MessagePolicy{KeepLatestDuplicates: true, ExcludeCMD: true, CompactSequences: true}
	root := t.TempDir()
	data, err := json.Marshal(plan)
	require.NoError(t, err)
	planPath := filepath.Join(root, "plan.json")
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	var out, stderr bytes.Buffer
	args := []string{"prepare", "--plan", planPath, "--workspace", filepath.Join(root, "prepare")}
	require.Equal(t, 0, migrationapp.Run(context.Background(), args, &out, &stderr), stderr.String())
	var p struct {
		migration.Preflight
		Mapping struct {
			Path, SHA256 string
			Rows         uint64
		} `json:"sequence_mapping"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &p))
	require.EqualValues(t, 4, p.Mapping.Rows)
	content, err := os.ReadFile(p.Mapping.Path)
	require.NoError(t, err)
	require.Equal(t, 4, len(bytes.Split(bytes.TrimSpace(content), []byte{'\n'})))
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(content)), p.Mapping.SHA256)

	out.Reset()
	stderr.Reset()
	exportArgs := append([]string{"export", "--plan", planPath, "--workspace", filepath.Join(root, "prepare")}, "--archive", filepath.Join(root, "archive"))
	require.Equal(t, 0, migrationapp.Run(context.Background(), exportArgs, &out, &stderr), stderr.String())
	exportArgs[0] = "export-map"
	exportArgs[4] = filepath.Join(root, "map-workspace")
	out.Reset()
	stderr.Reset()
	require.Equal(t, 0, migrationapp.Run(context.Background(), exportArgs, &out, &stderr), stderr.String())
	var mapping struct {
		Path, SHA256 string
		Rows         uint64
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &mapping))
	require.Equal(t, p.Mapping.SHA256, mapping.SHA256)
	require.NoDirExists(t, plan.Target.Nodes[0].DataDir)
	args[0] = "dedupe-plan"
	args[4] = filepath.Join(root, "plan")
	out.Reset()
	stderr.Reset()
	require.Equal(t, 0, migrationapp.Run(context.Background(), args, &out, &stderr), stderr.String())
	var d migration.DedupeReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &d))
	require.EqualValues(t, 1, d.Nodes[0].CMDDrops)
	require.EqualValues(t, 2, d.Nodes[0].Dropped)
	require.False(t, d.CutoverReady)
}
