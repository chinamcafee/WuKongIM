package migrationv2_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func TestOriginalSourceArchiveRoundTripPreservesEveryCapturedAndSelectedRecord(t *testing.T) {
	ctx := context.Background()
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	workspace, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "source"), "archive-roundtrip", 128<<20)
	require.NoError(t, err)
	defer workspace.Close()
	reader := migrationv2.Reader{}
	capture, err := migration.CaptureSources(ctx, []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: dir, ShardCount: 2}}}, reader, workspace, nil)
	require.NoError(t, err)
	catalog, err := migration.BuildSourceCatalog(ctx, capture, workspace, reader)
	require.NoError(t, err)
	selection, err := migration.SelectSources(ctx, capture, catalog, workspace, reader, nil)
	require.NoError(t, err)
	archivePath := filepath.Join(t.TempDir(), "archive")
	archive, err := archivefs.NewFileArchiveStore(archivePath)
	require.NoError(t, err)
	manifest, err := migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: "fixture-plan", SourceCommit: migrationv2.SourceCommit, ChunkBytes: 4096}, capture, catalog, selection, workspace, archive)
	require.NoError(t, err)
	require.Greater(t, len(manifest.Chunks), 1)
	resumed, err := migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: "fixture-plan", SourceCommit: migrationv2.SourceCommit, ChunkBytes: 4096}, capture, catalog, selection, workspace, archive)
	require.NoError(t, err)
	require.Equal(t, manifest, resumed)
	restored, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "restored"), "archive-roundtrip", 128<<20)
	require.NoError(t, err)
	defer restored.Close()
	read, err := migration.ReadSourceArchive(ctx, archive, func(row transfer.SpoolRow) error { return restored.Put(ctx, []transfer.SpoolRow{row}) })
	require.NoError(t, err)
	require.Equal(t, manifest, read)
	for _, prefix := range []string{"source/", "catalog/", "selected/"} {
		digest := func(spool *transfer.Spool) []byte {
			h := sha256.New()
			enc := json.NewEncoder(h)
			require.NoError(t, spool.Walk(ctx, []byte(prefix), func(row transfer.SpoolRow) error { return enc.Encode(row) }))
			return h.Sum(nil)
		}
		require.Equal(t, digest(workspace), digest(restored), prefix)
	}
	actual := map[string]int{}
	require.NoError(t, migration.WalkSelectedSources(ctx, restored, func(record migration.SelectedRecord) error {
		if record.Row.Kind == migration.Primary {
			actual[record.Row.Table]++
		}
		return nil
	}))
	require.Equal(t, 4, actual["Message"])
	require.Equal(t, 2, actual["Device"])
	changedOptions := migration.SourceArchiveOptions{PlanDigest: "different-plan", SourceCommit: migrationv2.SourceCommit, ChunkBytes: 4096}
	_, err = migration.ExportSourceArchive(ctx, changedOptions, capture, catalog, selection, workspace, archive)
	require.Error(t, err)
	completePath := filepath.Join(archivePath, "COMPLETE")
	complete, err := os.ReadFile(completePath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(completePath))
	_, err = migration.ReadSourceArchive(ctx, archive, func(transfer.SpoolRow) error { return nil })
	require.Error(t, err)
	require.NoError(t, os.WriteFile(completePath, complete, 0600))
	chunkPath := filepath.Join(archivePath, manifest.Chunks[0].Path)
	chunk, err := os.ReadFile(chunkPath)
	require.NoError(t, err)
	chunk[0] ^= 1
	require.NoError(t, os.WriteFile(chunkPath, chunk, 0600))
	visited := 0
	_, err = migration.ReadSourceArchive(ctx, archive, func(transfer.SpoolRow) error { visited++; return nil })
	require.Error(t, err)
	require.Zero(t, visited, "a corrupt first chunk must not reach consumers")
	_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: "fixture-plan", SourceCommit: migrationv2.SourceCommit, ChunkBytes: 4096}, capture, catalog, selection, workspace, archive)
	require.Error(t, err)
	unchanged, err := os.ReadFile(chunkPath)
	require.NoError(t, err)
	require.Equal(t, chunk, unchanged, "resume cannot overwrite the corrupt archive")

}
