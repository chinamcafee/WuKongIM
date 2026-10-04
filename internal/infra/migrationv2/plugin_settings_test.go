package migrationv2_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
	"github.com/stretchr/testify/require"
)

func originalPluginSettingsRow(t *testing.T) migration.Row {
	t.Helper()
	var fixtures map[string][]struct{ Key, Value []byte }
	data, err := os.ReadFile("testdata/original-v2-plugin-kv.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &fixtures))
	row := migration.Row{Table: "Plugin", Kind: migration.Primary, Fields: map[string][]byte{}}
	names := map[uint16]string{0x1501: "No", 0x1502: "Name", 0x1503: "ConfigTemplate", 0x1504: "CreatedAt", 0x1505: "UpdatedAt", 0x1506: "Status", 0x1507: "Version", 0x1508: "Methods", 0x1509: "Priority", 0x150a: "Config"}
	for _, kv := range fixtures["config_only"] {
		if kv.Key[2] != byte(migration.Primary) {
			continue
		}
		row.Key = append([]byte(nil), kv.Key[:12]...)
		row.ID = binary.BigEndian.Uint64(kv.Key[4:12])
		row.Fields[names[binary.BigEndian.Uint16(kv.Key[12:])]] = append([]byte(nil), kv.Value...)
	}
	return row
}

func TestPluginNodeSettingsPreserveEveryConfigAndNativeStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "plugin-settings", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit}
	capture := migration.SourceCapture{Digest: "synthetic-complete-capture"}
	created := time.Unix(1750000000, 123456789).UTC()
	updated := created.Add(time.Second)
	for node := uint64(1); node <= 3; node++ {
		// Reverse target order proves assignment is explicit, independent of sort.
		plan.PluginNodes = append(plan.PluginNodes, migration.PluginNodeMapping{SourceNode: 1000 + node, TargetNode: 4 - node})
		plan.Sources = append(plan.Sources, migration.NodeOptions{NodeID: 1000 + node})
		plan.Target.Nodes = append(plan.Target.Nodes, migration.TargetNode{NodeID: node})
		capture.Nodes = append(capture.Nodes, migration.NodeSnapshot{NodeID: 1000 + node})
		row := originalPluginSettingsRow(t)
		row.Fields["Config"] = []byte(fmt.Sprintf(`{"name":"secret-node-%d","n":9007199254740993}`, node))
		row.Fields["CreatedAt"] = binary.BigEndian.AppendUint64(nil, uint64(created.UnixNano()))
		row.Fields["UpdatedAt"] = binary.BigEndian.AppendUint64(nil, uint64(updated.UnixNano()))
		if node == 2 {
			row.Fields["Status"] = []byte{0, 0, 0, 2}
		}
		data, err := json.Marshal(row)
		require.NoError(t, err)
		require.NoError(t, w.Put(ctx, []transfer.SpoolRow{{Key: []byte(fmt.Sprintf("source/%020d/rows/0000/%x", 1000+node, row.Key)), Value: data}}))
	}
	r := migrationv2.Reader{}
	report, err := migration.PreparePluginSettings(ctx, plan, capture, w, r)
	require.NoError(t, err)
	require.Equal(t, uint64(3), report.Records)
	data, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-node")
	var records []migration.MappedPluginSettings
	require.NoError(t, migration.WalkPluginSettings(ctx, w, *report, func(record migration.MappedPluginSettings) error {
		records = append(records, record)
		require.Equal(t, uint64(1004)-record.SourceNode, record.TargetNode)
		want := fmt.Sprintf(`{"name":"secret-node-%d","n":9007199254740993}`, record.SourceNode-1000)
		require.JSONEq(t, want, string(record.Desired.Config))
		require.Equal(t, created, record.Desired.CreatedAt)
		require.Equal(t, updated, record.Desired.UpdatedAt)
		require.Equal(t, record.SourceNode != 1002, record.Desired.Enabled)
		store := pluginhost.NewStore(filepath.Join(t.TempDir(), "state"))
		require.NoError(t, store.Save(record.Desired))
		got, err := store.Load(record.Desired.No)
		require.NoError(t, err)
		require.JSONEq(t, want, string(got.Config))
		require.Equal(t, record.Desired.CreatedAt, got.CreatedAt)
		require.Equal(t, record.Desired.Enabled, got.Enabled)
		return nil
	}))
	again, err := migration.PreparePluginSettings(ctx, plan, capture, w, r)
	require.NoError(t, err)
	require.Equal(t, report, again)
	for _, kind := range []string{"merge", "missing", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			bad := plan
			bad.PluginNodes = append([]migration.PluginNodeMapping(nil), plan.PluginNodes...)
			switch kind {
			case "merge":
				bad.PluginNodes[0].TargetNode = bad.PluginNodes[1].TargetNode
			case "missing":
				bad.PluginNodes = bad.PluginNodes[:2]
			case "unknown":
				bad.PluginNodes[0].SourceNode = 99
			}
			_, err := migration.PreparePluginSettings(ctx, bad, capture, w, r)
			require.Error(t, err)
		})
	}
	// Changed private output must fail before a consumer can use a record.
	records[0].Desired.Config = []byte(`{"name":"changed"}`)
	changed, err := migration.MarshalState(records[0])
	require.NoError(t, err)
	key := fmt.Sprintf("plugin-settings/v1/%s/%s/%020d/%x", report.CaptureDigest, report.PlanDigest, records[0].TargetNode, []byte(records[0].Desired.No))
	require.ErrorContains(t, w.Put(ctx, []transfer.SpoolRow{{Key: []byte(key), Value: changed}}), "durable key conflict")
	visited := false
	err = migration.WalkPluginSettings(ctx, alteredPluginSettingsWorkspace{Workspace: w, key: key, value: changed}, *report, func(migration.MappedPluginSettings) error { visited = true; return nil })
	require.ErrorContains(t, err, "digest mismatch")
	require.False(t, visited)
	for _, seal := range []string{"workflow/PREPARED", "conversion/COMPLETE"} {
		_, found, err := w.Get(ctx, []byte(seal))
		require.NoError(t, err)
		require.False(t, found)
	}
}

type alteredPluginSettingsWorkspace struct {
	migration.Workspace
	key   string
	value []byte
}

func (w alteredPluginSettingsWorkspace) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	return w.Workspace.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		if string(row.Key) == w.key {
			row.Value = w.value
		}
		return visit(row)
	})
}

func TestOriginalPluginSettingsRejectIdentityAndUnknownFields(t *testing.T) {
	for _, kind := range []string{"wrong-node-shard", "identity", "unknown-field", "malformed-config", "path"} {
		t.Run(kind, func(t *testing.T) {
			row := originalPluginSettingsRow(t)
			switch kind {
			case "wrong-node-shard":
				row.Shard = 1
			case "identity":
				row.ID++
			case "unknown-field":
				row.Fields["FutureBusinessField"] = []byte("secret")
			case "malformed-config":
				row.Fields["Config"] = []byte(`"secret"`)
			case "path":
				row.Fields["No"] = []byte("../unsafe")
			}
			_, err := (migrationv2.Reader{}).DecodeBusiness(row, migration.RecordIdentity{})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
		})
	}
}
