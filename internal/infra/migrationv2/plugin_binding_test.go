package migrationv2_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func TestOriginalPluginBindingDecodesAndValidatesBothOperationalIndexes(t *testing.T) {
	var fixture struct {
		UID               string                        `json:"uid"`
		PluginNo          string                        `json:"plugin_no"`
		CreatedAtNS       int64                         `json:"created_at_ns"`
		UserPluginNoEmpty bool                          `json:"user_plugin_no_empty"`
		Exists            bool                          `json:"exists"`
		Rows              []struct{ Key, Value []byte } `json:"rows"`
	}
	data, err := os.ReadFile("testdata/original-v2-plugin-bindings.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.True(t, fixture.UserPluginNoEmpty)
	require.True(t, fixture.Exists, "original runtime reads PluginUser despite the empty legacy User column")
	for _, variant := range []string{"valid", "missing-uid-index", "missing-plugin-index", "changed-primary-identity", "orphan-index"} {
		t.Run(variant, func(t *testing.T) {
			ctx, source := context.Background(), compatibleMessageFixture(t)
			db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", "shard000"), &pebble.Options{ErrorIfNotExists: true})
			require.NoError(t, err)
			for _, row := range fixture.Rows {
				name := binary.BigEndian.Uint16(row.Key[4:6])
				if row.Key[2] == byte(migration.Index) && ((variant == "missing-uid-index" && name == 0x1601) || (variant == "missing-plugin-index" && name == 0x1602)) {
					continue
				}
				value := row.Value
				if variant == "changed-primary-identity" && len(row.Key) == 14 && binary.BigEndian.Uint16(row.Key[12:]) == 0x1602 {
					value = []byte("different-user")
				}
				if variant == "orphan-index" && row.Key[2] == byte(migration.Primary) {
					continue
				}
				require.NoError(t, db.Set(row.Key, value, pebble.Sync))
			}
			require.NoError(t, db.Close())
			before := fileDigests(t, source)
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "plugin-binding-"+variant, 128<<20)
			require.NoError(t, err)
			defer w.Close()
			r := migrationv2.Reader{}
			nodes := []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}
			capture, err := migration.CaptureSources(ctx, nodes, r, w, nil)
			require.NoError(t, err)
			catalog, err := migration.BuildSourceCatalog(ctx, capture, w, r)
			if variant == "changed-primary-identity" {
				require.ErrorContains(t, err, "plugin binding identity does not match its key")
				return
			}
			require.NoError(t, err)
			err = migration.ValidateSourceIndexes(ctx, capture, nodes, w, r)
			if variant != "valid" {
				if variant == "orphan-index" {
					require.ErrorContains(t, err, "orphaned")
				} else {
					require.ErrorContains(t, err, "index is missing")
				}
				return
			}
			require.NoError(t, err)
			selection, err := migration.SelectSources(ctx, capture, catalog, w, r, nil)
			require.NoError(t, err)
			found := false
			require.NoError(t, migration.WalkSelectedSources(ctx, w, func(row migration.SelectedRecord) error {
				if row.Row.Table != "PluginUser" {
					return nil
				}
				facts, err := r.DecodeBusiness(row.Row, row.Identity)
				require.NoError(t, err)
				require.Equal(t, &migration.SourcePluginBinding{SourceID: counterHash(fixture.PluginNo + "_" + fixture.UID), UID: fixture.UID, PluginNo: fixture.PluginNo, CreatedAtNS: fixture.CreatedAtNS, UpdatedAtNS: fixture.CreatedAtNS}, facts.PluginBinding)
				found = true
				return nil
			}))
			require.True(t, found)
			converted, err := migration.BuildTargetRecords(ctx, selection, w, r)
			require.NoError(t, err)
			require.Equal(t, uint64(1), converted.Metadata["plugin_binding"])
			require.Equal(t, before, fileDigests(t, source))
		})
	}
}
