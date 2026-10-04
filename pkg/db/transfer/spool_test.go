package transfer_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func TestMigrationSpoolResumesExactRowsAndRejectsChangedIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	s, err := transfer.OpenSpool(dir, "fixture-migration", 1024)
	require.NoError(t, err)
	require.NoError(t, s.Put(context.Background(), []transfer.SpoolRow{
		{Key: []byte("source/2/user/b"), Value: []byte("bob")},
		{Key: []byte("source/1/user/a"), Value: []byte("alice")},
	}))
	require.NoError(t, s.Close())
	_, err = transfer.OpenSpool(dir, "different-migration", 1024)
	require.ErrorContains(t, err, "identity")
	s, err = transfer.OpenSpool(dir, "fixture-migration", 1024)
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.Put(context.Background(), []transfer.SpoolRow{{Key: []byte("source/1/user/a"), Value: []byte("alice")}}))
	require.ErrorContains(t, s.Put(context.Background(), []transfer.SpoolRow{{Key: []byte("source/1/user/a"), Value: []byte("mallory")}}), "conflict")
	var rows []string
	require.NoError(t, s.Walk(context.Background(), []byte("source/"), func(row transfer.SpoolRow) error { rows = append(rows, string(row.Value)); return nil }))
	require.Equal(t, []string{"alice", "bob"}, rows)
}
