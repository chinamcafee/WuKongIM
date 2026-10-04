package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"

	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	"github.com/cockroachdb/pebble"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/stretchr/testify/require"
)

func TestAuthorityDecodesOriginalRetainedConfigCommands(t *testing.T) {
	source := unpackNamedFixture(t, "original-v2-server.tar.gz")
	before := fileDigests(t, source)
	r := migrationv2.Reader{}
	configs := map[uint64]migration.ChannelConfigEvidence{}
	var logs []migration.ChannelConfigLog
	snapshot, err := r.ReadAuthorityNode(context.Background(), migration.NodeOptions{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}, func(row migration.Row) error {
		if row.Table == "ChannelClusterConfig" && row.Kind == migration.Primary {
			c, err := r.InspectChannelConfig(row)
			if err != nil {
				return err
			}
			configs[c.Owner] = c
		}
		return nil
	}, nil, func(log migration.ChannelConfigLog) error { logs = append(logs, log); return nil })
	require.NoError(t, err)
	require.NotEmpty(t, logs)
	matched := 0
	for _, log := range logs {
		require.Empty(t, log.DecodeErrorSHA256)
		c, ok := configs[log.Config.Owner]
		if ok && c.Version == log.Index {
			require.Equal(t, c.SHA256, log.Config.SHA256)
			require.NotEmpty(t, c.NonMigrationSHA256)
			require.Equal(t, c.NonMigrationSHA256, log.Config.NonMigrationSHA256)
			matched++
		}
		require.LessOrEqual(t, log.Index, snapshot.SlotProgress[log.Slot].AppliedIndex)
	}
	require.Equal(t, len(configs), matched)
	require.Equal(t, before, fileDigests(t, source))
}

func TestAuthorityHistoryDigestExcludesOnlyVersionAndMigrationMarkers(t *testing.T) {
	source := unpackNamedFixture(t, "original-v2-server.tar.gz")
	r := migrationv2.Reader{}
	var original migration.Row
	require.NoError(t, migrationv2.Scan(context.Background(), migration.Options{DataDir: source, ShardCount: 2}, func(row migration.Row) error {
		if row.Table == "ChannelClusterConfig" && row.Kind == migration.Primary {
			original = row
		}
		return nil
	}))
	require.NotEmpty(t, original.Fields)
	baseline, err := r.InspectChannelConfig(original)
	require.NoError(t, err)
	for _, field := range []string{"ConfVersion", "MigrateFrom", "MigrateTo", "CreatedAt", "UpdatedAt", "UnknownOriginalField"} {
		t.Run(field, func(t *testing.T) {
			row := original
			row.Fields = map[string][]byte{}
			for k, v := range original.Fields {
				row.Fields[k] = bytes.Clone(v)
			}
			v := make([]byte, 8)
			binary.BigEndian.PutUint64(v, 1732364897806218890)
			row.Fields[field] = v
			changed, err := r.InspectChannelConfig(row)
			require.NoError(t, err)
			require.NotEqual(t, baseline.SHA256, changed.SHA256)
			if field == "ConfVersion" || field == "MigrateFrom" || field == "MigrateTo" {
				require.Equal(t, baseline.NonMigrationSHA256, changed.NonMigrationSHA256)
			} else {
				require.NotEqual(t, baseline.NonMigrationSHA256, changed.NonMigrationSHA256)
			}
		})
	}
}

func TestAuthorityCLIReportsUnprovenMarkersAndKeepsPrepareStrict(t *testing.T) {
	source := unpackNamedFixture(t, "original-v2-server.tar.gz")
	changed := rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) == 14 && binary.BigEndian.Uint16(key) == 0x0b01 && key[2] == 1 {
			col := binary.BigEndian.Uint16(key[12:])
			if col == 0x0b08 || col == 0x0b09 {
				v := make([]byte, 8)
				binary.BigEndian.PutUint64(v, 1)
				require.NoError(t, b.Set(key, v, nil))
				return true
			}
		}
		return false
	})
	require.Positive(t, changed)
	before := fileDigests(t, source)
	plan := diagnosticPlan(t, source)
	root := t.TempDir()
	planPath := filepath.Join(root, "plan.json")
	data, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	args := []string{"authority", "--plan", planPath, "--workspace", filepath.Join(root, "authority")}
	var out, stderr bytes.Buffer
	require.Equal(t, 1, migrationapp.Run(context.Background(), args, &out, &stderr), stderr.String())
	var report migration.AuthorityReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	require.True(t, report.ScanComplete)
	require.True(t, report.TopologyChecked)
	require.Positive(t, report.Channels)
	require.False(t, report.CutoverReady)
	require.Equal(t, report.Channels, report.Classes["insufficient_evidence"])
	first := append([]byte{}, out.Bytes()...)
	out.Reset()
	stderr.Reset()
	require.Equal(t, 1, migrationapp.Run(context.Background(), args, &out, &stderr), stderr.String())
	require.Equal(t, string(first), out.String())
	args[0] = "prepare"
	out.Reset()
	stderr.Reset()
	require.Equal(t, 1, migrationapp.Run(context.Background(), args, &out, &stderr))
	require.Contains(t, stderr.String(), "identity")
	require.Equal(t, before, fileDigests(t, source))
	_, err = os.Stat(plan.Target.Nodes[0].DataDir)
	require.True(t, os.IsNotExist(err))
}
