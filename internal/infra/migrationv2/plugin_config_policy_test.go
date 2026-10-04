package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// pluginConfigFixture is a bounded component fixture derived from original
// public-API columns. Its capture is explicitly synthetic, not full preflight.
func pluginConfigFixture(t *testing.T, missingNode uint64) (migration.Plan, migration.SourceCapture, *transfer.Spool, string) {
	t.Helper()
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "config-policy", 128<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Close()) })
	p := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit}
	c := migration.SourceCapture{Digest: "synthetic-plugin-component"}
	no := string(originalPluginSettingsRow(t).Fields["No"])
	for n := uint64(1); n <= 3; n++ {
		p.Sources = append(p.Sources, migration.NodeOptions{NodeID: 1000 + n})
		p.Target.Nodes = append(p.Target.Nodes, migration.TargetNode{NodeID: 4 - n})
		p.PluginNodes = append(p.PluginNodes, migration.PluginNodeMapping{SourceNode: 1000 + n, TargetNode: 4 - n})
		c.Nodes = append(c.Nodes, migration.NodeSnapshot{NodeID: 1000 + n})
		for _, pluginNo := range []string{no, "untouched-plugin"} {
			if n == missingNode && pluginNo == no {
				continue
			}
			row := originalPluginSettingsRow(t)
			row.Fields["No"] = []byte(pluginNo)
			row.ID = counterHash(pluginNo)
			binary.BigEndian.PutUint64(row.Key[4:], row.ID)
			row.Fields["Config"] = []byte(fmt.Sprintf(`{"name":"secret-%d","large":9007199254740993}`, n))
			row.Fields["Status"] = binary.BigEndian.AppendUint32(nil, uint32(n-1))
			row.Fields["CreatedAt"] = binary.BigEndian.AppendUint64(nil, uint64(1700000000000000000+int64(n)))
			row.Fields["UpdatedAt"] = binary.BigEndian.AppendUint64(nil, uint64(1700000001000000000+int64(n)))
			data, err := json.Marshal(row)
			require.NoError(t, err)
			require.NoError(t, w.Put(ctx, []transfer.SpoolRow{{Key: []byte(fmt.Sprintf("source/%020d/rows/0000/%x", 1000+n, row.Key)), Value: data}}))
		}
	}
	p.PluginConfigs = []migration.PluginConfigMapping{{PluginNo: no, SourceNode: 1001}}
	return p, c, w, no
}

type pluginStateInspector struct {
	states map[uint64][]pluginhost.DesiredState
}

func (i pluginStateInspector) Open(_ context.Context, _ migration.TargetPlan, n migration.TargetNode) (migration.TargetView, error) {
	return pluginStateView{states: i.states[n.NodeID]}, nil
}

type pluginStateView struct {
	migration.TargetView
	states []pluginhost.DesiredState
}

func (v pluginStateView) Close() error { return nil }
func (v pluginStateView) WalkPluginStates(_ context.Context, visit func(pluginhost.DesiredState) error) error {
	for _, s := range v.states {
		if err := visit(s); err != nil {
			return err
		}
	}
	return nil
}

func TestPluginConfigPolicyPreservesOriginalAndVerifiesIndependently(t *testing.T) {
	ctx := context.Background()
	plan, c, w, no := pluginConfigFixture(t, 0)
	r := migrationv2.Reader{}
	report, err := migration.PreparePluginSettings(ctx, plan, c, w, r)
	require.NoError(t, err)
	require.Equal(t, uint64(6), report.Records)
	native := pluginStateInspector{states: map[uint64][]pluginhost.DesiredState{}}
	var selectedRow string
	require.NoError(t, migration.WalkPluginSettings(ctx, w, *report, func(rec migration.MappedPluginSettings) error {
		n := rec.SourceNode - 1000
		require.Contains(t, string(rec.Original.Config), fmt.Sprintf("secret-%d", n))
		require.Equal(t, rec.Original.Status != 2, rec.Desired.Enabled)
		require.Equal(t, *rec.Original.CreatedAt, rec.Desired.CreatedAt)
		require.Equal(t, *rec.Original.UpdatedAt, rec.Desired.UpdatedAt)
		wantNode := n
		if rec.Desired.No == no {
			wantNode = 1
			require.NotNil(t, rec.ConfigSource)
			require.Equal(t, uint64(1001), rec.ConfigSource.SourceNode)
			if selectedRow == "" {
				selectedRow = rec.ConfigSource.SourceRowSHA256
			}
			require.Equal(t, selectedRow, rec.ConfigSource.SourceRowSHA256)
			if rec.SourceNode == 1001 {
				require.Equal(t, rec.SourceRowSHA256, selectedRow)
				require.Equal(t, rec.SourceKey, rec.ConfigSource.SourceKey)
			}
		} else {
			require.Nil(t, rec.ConfigSource)
		}
		require.Contains(t, string(rec.Desired.Config), fmt.Sprintf("secret-%d", wantNode))
		store := pluginhost.NewStore(filepath.Join(t.TempDir(), "plugin-state"))
		require.NoError(t, store.Save(rec.Desired))
		got, err := store.Load(rec.Desired.No)
		require.NoError(t, err)
		native.states[rec.TargetNode] = append(native.states[rec.TargetNode], got)
		return nil
	}))
	// Assignment corruption is invisible to the independent source verifier.
	untrusted := ignoreAssignedSettingsWorkspace{Workspace: w}
	verified, err := migration.VerifyPluginSettings(ctx, plan, c, untrusted, r, native)
	require.NoError(t, err)
	require.Equal(t, uint64(6), verified.Records)
	again, err := migration.PreparePluginSettings(ctx, plan, c, w, r)
	require.NoError(t, err)
	require.Equal(t, report, again)
	repeat, err := migration.VerifyPluginSettings(ctx, plan, c, untrusted, r, native)
	require.NoError(t, err)
	require.Equal(t, verified, repeat)
	for _, kind := range []string{"config", "large-number", "enabled", "created", "updated", "missing", "extra"} {
		t.Run(kind, func(t *testing.T) {
			bad := pluginStateInspector{states: map[uint64][]pluginhost.DesiredState{}}
			for node, states := range native.states {
				bad.states[node] = append([]pluginhost.DesiredState(nil), states...)
			}
			s := &bad.states[1][0]
			switch kind {
			case "config":
				s.Config = []byte(`{"name":"wrong-secret"}`)
			case "large-number":
				s.Config = []byte(strings.Replace(string(s.Config), "9007199254740993", "9007199254740992", 1))
			case "enabled":
				s.Enabled = !s.Enabled
			case "created":
				s.CreatedAt = s.CreatedAt.Add(time.Nanosecond)
			case "updated":
				s.UpdatedAt = s.UpdatedAt.Add(time.Nanosecond)
			case "missing":
				bad.states[1] = bad.states[1][1:]
			case "extra":
				s2 := *s
				s2.No = "unexpected"
				bad.states[1] = append(bad.states[1], s2)
			}
			_, err := migration.VerifyPluginSettings(ctx, plan, c, untrusted, r, bad)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
		})
	}
	// Applying a different policy to the same installed files must not verify.
	different := plan
	different.PluginConfigs = nil
	_, err = migration.VerifyPluginSettings(ctx, different, c, w, r, native)
	require.Error(t, err)
	data, err := json.Marshal(struct {
		Assignment *migration.PluginSettingsReport
		Verified   migration.PluginSettingsVerification
	}{report, verified})
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret")
}

type ignoreAssignedSettingsWorkspace struct{ migration.Workspace }

func (w ignoreAssignedSettingsWorkspace) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	if len(prefix) >= 15 && string(prefix[:15]) == "plugin-settings" {
		panic("verification must not read converted plugin settings")
	}
	return w.Workspace.Walk(ctx, prefix, visit)
}

func TestPluginConfigPolicyRejectsIncompleteOrAmbiguousChoice(t *testing.T) {
	for _, kind := range []string{"unknown-source", "duplicate", "unsafe-no", "unknown-plugin", "no-map", "missing-selected", "missing-other"} {
		t.Run(kind, func(t *testing.T) {
			var missing uint64
			if kind == "missing-selected" {
				missing = 1
			}
			if kind == "missing-other" {
				missing = 3
			}
			p, c, w, _ := pluginConfigFixture(t, missing)
			switch kind {
			case "unknown-source":
				p.PluginConfigs[0].SourceNode = 99
			case "duplicate":
				p.PluginConfigs = append(p.PluginConfigs, p.PluginConfigs[0])
			case "unsafe-no":
				p.PluginConfigs[0].PluginNo = "../outside"
			case "unknown-plugin":
				p.PluginConfigs[0].PluginNo = "absent"
			case "no-map":
				p.PluginNodes = nil
			}
			_, err := migration.PreparePluginSettings(context.Background(), p, c, w, migrationv2.Reader{})
			require.Error(t, err)
			for _, seal := range []string{"workflow/PREPARED", "conversion/COMPLETE"} {
				_, found, err := w.Get(context.Background(), []byte(seal))
				require.NoError(t, err)
				require.False(t, found)
			}
		})
	}
}

func TestPluginConfigPolicyCLIArchiveImportAndVerification(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := compatibleMessageFixture(t)
	var fixtures map[string][]struct{ Key, Value []byte }
	raw, err := os.ReadFile("testdata/original-v2-plugin-kv.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fixtures))
	db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", "shard000"), &pebble.Options{ErrorIfNotExists: true})
	require.NoError(t, err)
	var no string
	for _, row := range fixtures["descriptor"] {
		require.NoError(t, db.Set(row.Key, row.Value, pebble.Sync))
		if row.Key[2] == byte(migration.Primary) && binary.BigEndian.Uint16(row.Key[len(row.Key)-2:]) == 0x1501 {
			no = string(row.Value)
		}
	}
	require.NoError(t, db.Close())
	require.NotEmpty(t, no)
	p := diagnosticPlan(t, source)
	p.PluginNodes = []migration.PluginNodeMapping{{SourceNode: 1, TargetNode: p.Target.Nodes[0].NodeID}}
	p.PluginConfigs = []migration.PluginConfigMapping{{PluginNo: no, SourceNode: 1}}
	raw, err = json.Marshal(p)
	require.NoError(t, err)
	planPath := filepath.Join(root, "plan.json")
	require.NoError(t, os.WriteFile(planPath, raw, 0600))
	run := func(verb, workspace string) (int, []byte, string) {
		var out, diag bytes.Buffer
		args := []string{verb, "--plan", planPath, "--workspace", filepath.Join(root, workspace)}
		if verb != "prepare" {
			args = append(args, "--archive", filepath.Join(root, "archive"))
		}
		code := migrationapp.Run(ctx, args, &out, &diag)
		return code, out.Bytes(), diag.String()
	}
	for _, verb := range []string{"prepare", "export"} {
		code, _, diag := run(verb, "source-workspace")
		require.Zero(t, code, diag)
	}
	require.NoError(t, os.Rename(source, source+"-unmounted"))
	code, _, diag := run("import", "target-workspace")
	require.Zero(t, code, diag)
	state, err := pluginhost.NewStore(filepath.Join(p.Target.Nodes[0].DataDir, "plugin-state")).Load(no)
	require.NoError(t, err)
	require.Equal(t, no, state.No)
	code, out, diag := run("verify", "verification-workspace")
	require.Zero(t, code, diag)
	var verified migration.VerificationReport
	require.NoError(t, json.Unmarshal(out, &verified))
	require.NotNil(t, verified.PluginSettings)
	require.Equal(t, uint64(1), verified.PluginSettings.Records)
	require.Len(t, verified.PluginSettings.Digest, 64)
	require.False(t, verified.CutoverReady)
	require.NoError(t, os.Remove(filepath.Join(p.Target.Nodes[0].DataDir, "plugin-state", no+".json")))
	code, _, diag = run("verify", "verification-workspace")
	require.NotZero(t, code)
	require.Contains(t, diag, "plugin settings count mismatch")
}
