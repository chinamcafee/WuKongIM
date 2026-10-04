package migrationv2_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func TestCapturedOriginalSourceBuildsACompleteDiskIdentityCatalog(t *testing.T) {
	ctx := context.Background()
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	spool, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "original-source-catalog", 128<<20)
	require.NoError(t, err)
	defer spool.Close()
	capture, err := migration.CaptureSources(ctx, []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: dir, ShardCount: 2}}}, migrationv2.Reader{}, spool, nil)
	require.NoError(t, err)
	catalog, err := migration.BuildSourceCatalog(ctx, capture, spool, migrationv2.Reader{})
	require.NoError(t, err)
	require.Equal(t, uint64(4), catalog.Channels, "two explicit channels plus two account personal-permission identities")
	require.Equal(t, uint64(4), catalog.UIDs)
	require.Equal(t, uint64(4), catalog.EventMessages)
	resumed, err := migration.BuildSourceCatalog(ctx, capture, spool, migrationv2.Reader{})
	require.NoError(t, err)
	require.Equal(t, catalog, resumed)
}
