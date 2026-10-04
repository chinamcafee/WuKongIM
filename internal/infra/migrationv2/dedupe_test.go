package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	migrationapp "github.com/WuKongIM/WuKongIM/internal/app/migration"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/stretchr/testify/require"
)

func TestDedupeOriginalFixtureClientScopeAndCLI(t *testing.T) {
	source := unpackNamedFixture(t, "original-v2-server.tar.gz")
	before := fileDigests(t, source)
	var row migration.Row
	r := migrationv2.Reader{}
	err := migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: source, ShardCount: 2}, func(v migration.Row) error {
		if v.Table == "Message" && v.Kind == migration.Primary {
			row = v
		}
		return nil
	})
	require.NoError(t, err)
	row.Fields["FromUid"] = []byte("sender-a")
	row.Fields["ClientMsgNo"] = []byte("retry")
	a, err := r.InspectDedupeMessage(row, 2)
	require.NoError(t, err)
	require.NotEmpty(t, a.ClientKeySHA256)
	row.Fields["Header"] = []byte{4}
	cmd, err := r.InspectDedupeMessage(row, 2)
	require.NoError(t, err)
	require.True(t, cmd.CMD)
	require.Contains(t, cmd.UnsupportedFields, "sync_once")
	row.Fields["Header"] = []byte{2}
	flagged, err := r.InspectDedupeMessage(row, 2)
	require.NoError(t, err)
	require.NotContains(t, flagged.UnsupportedFields, "red_dot")
	require.NotContains(t, flagged.UnsupportedFields, "sync_once")
	row.Fields["Header"] = []byte{0}
	row.Fields["FromUid"] = []byte("sender-b")
	b, err := r.InspectDedupeMessage(row, 2)
	require.NoError(t, err)
	require.NotEqual(t, a.ClientKeySHA256, b.ClientKeySHA256)
	for _, field := range []string{"FromUid", "ClientMsgNo"} {
		saved := row.Fields[field]
		row.Fields[field] = nil
		m, err := r.InspectDedupeMessage(row, 2)
		require.NoError(t, err)
		require.Empty(t, m.ClientKeySHA256)
		row.Fields[field] = saved
	}
	root := t.TempDir()
	p := diagnosticPlan(t, source)
	data, err := json.Marshal(p)
	require.NoError(t, err)
	planPath := filepath.Join(root, "plan.json")
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	args := []string{"dedupe-plan", "--plan", planPath, "--workspace", filepath.Join(root, "scratch")}
	var out, stderr bytes.Buffer
	require.Equal(t, 0, migrationapp.Run(context.Background(), args, &out, &stderr), stderr.String())
	var report migration.DedupeReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	require.True(t, report.ScanComplete)
	require.Equal(t, 5, report.Version)
	require.EqualValues(t, 4, report.Nodes[0].Protocol.Retained)
	require.False(t, report.CutoverReady)
	require.EqualValues(t, 4, report.Nodes[0].Messages)
	first := out.String()
	out.Reset()
	require.Equal(t, 0, migrationapp.Run(context.Background(), args, &out, &stderr), stderr.String())
	require.Equal(t, first, out.String())
	args[0] = "prepare"
	out.Reset()
	stderr.Reset()
	require.Equal(t, 1, migrationapp.Run(context.Background(), args, &out, &stderr))
	require.Contains(t, stderr.String(), "identity")
	require.Equal(t, before, fileDigests(t, source))
	_, err = os.Stat(p.Target.Nodes[0].DataDir)
	require.True(t, os.IsNotExist(err))
}
