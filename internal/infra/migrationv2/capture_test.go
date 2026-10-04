package migrationv2_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func TestCaptureOriginalStoppedSourceResumesOnlyUnchangedInventory(t *testing.T) {
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	spool, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "original-source-capture", 128<<20)
	require.NoError(t, err)
	defer spool.Close()
	nodes := []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: dir, ShardCount: 2}}}
	first, err := migration.CaptureSources(context.Background(), nodes, migrationv2.Reader{}, spool, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(4), first.Tables["Message"])
	require.Equal(t, uint64(2), first.Tables["PendingConversation"])
	second, err := migration.CaptureSources(context.Background(), nodes, migrationv2.Reader{}, spool, nil)
	require.NoError(t, err)
	require.Equal(t, first, second)
	// A newly appearing source file after an interrupted attempt changes its
	// bound inventory even if all existing business rows still match.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "unexpected-tail"), []byte("new data"), 0600))
	_, err = migration.CaptureSources(context.Background(), nodes, migrationv2.Reader{}, spool, nil)
	require.ErrorContains(t, err, "conflict")
}

func TestCaptureRejectsUnconvergedOriginalThreeNodeCluster(t *testing.T) {
	spool, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "unconverged-original-cluster", 128<<20)
	require.NoError(t, err)
	defer spool.Close()
	var nodes []migration.NodeOptions
	for _, id := range []uint64{11, 22, 33} {
		dir := unpackNamedFixture(t, fmt.Sprintf("original-v2-unconverged-%d.tar.gz", id))
		nodes = append(nodes, migration.NodeOptions{NodeID: id, Options: migration.Options{DataDir: dir, ShardCount: 2}})
	}
	_, err = migration.CaptureSources(context.Background(), nodes, migrationv2.Reader{}, spool, nil)
	require.ErrorContains(t, err, "source Slot 13 leader has an unapplied log tail")
}
