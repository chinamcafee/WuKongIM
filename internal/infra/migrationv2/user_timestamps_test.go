package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// alterUserDisplayTimes changes only synthetic fixture copies, maintaining the
// exact original primary/secondary index relationship on every source node.
func alterUserDisplayTimes(t *testing.T, dir string, delta uint64) {
	t.Helper()
	rewriteOriginalIndexFixture(t, dir, func(key, value []byte, b *pebble.Batch) bool {
		if len(key) < 4 || binary.BigEndian.Uint16(key) != 0x0201 {
			return false
		}
		if key[2] == byte(migration.Primary) && len(key) == 14 {
			col := binary.BigEndian.Uint16(key[12:])
			if col != 0x0209 && col != 0x020a {
				return false
			}
			require.NoError(t, b.Set(key, binary.BigEndian.AppendUint64(nil, binary.BigEndian.Uint64(value)+delta), nil))
			return true
		}
		if key[2] == byte(migration.SecondaryIndex) && len(key) == 22 {
			index := binary.BigEndian.Uint16(key[4:])
			if index != 0x0201 && index != 0x0202 {
				return false
			}
			changed := bytes.Clone(key)
			binary.BigEndian.PutUint64(changed[6:14], binary.BigEndian.Uint64(key[6:14])+delta)
			require.NoError(t, b.Delete(key, nil))
			require.NoError(t, b.Set(changed, value, nil))
			return true
		}
		return false
	})
}

func TestUserTimestampArchiveKeepsBusinessComparisonStrict(t *testing.T) {
	for _, fault := range []string{"strict", "archive", "plugin-binding", "device-token"} {
		t.Run(fault, func(t *testing.T) {
			ctx, r := context.Background(), migrationv2.Reader{}
			p := diagnosticPlan(t, "")
			p.Sources = nil
			for n := 1; n <= 3; n++ {
				dir := unpackNamedFixture(t, fmt.Sprintf("original-v2-three-%d.tar.gz", n))
				clearFixtureMessageExtensions(t, dir)
				alterUserDisplayTimes(t, dir, uint64(n)*17)
				if n == 1 && fault == "plugin-binding" {
					rewriteOriginalIndexFixture(t, dir, func(key, value []byte, b *pebble.Batch) bool {
						if len(key) == 14 && binary.BigEndian.Uint16(key) == 0x0201 && key[2] == byte(migration.Primary) && binary.BigEndian.Uint16(key[12:]) == 0x0201 {
							changed := bytes.Clone(key)
							binary.BigEndian.PutUint16(changed[12:], 0x020b)
							require.NoError(t, b.Set(changed, []byte("different-business-plugin"), nil))
							return true
						}
						return false
					})
				}
				if n == 1 && fault == "device-token" {
					rewriteOriginalIndexFixture(t, dir, func(key, value []byte, b *pebble.Batch) bool {
						if len(key) == 14 && binary.BigEndian.Uint16(key) == 0x0301 && key[2] == byte(migration.Primary) && binary.BigEndian.Uint16(key[12:]) == 0x0302 {
							require.NoError(t, b.Set(key, []byte("different-device-token"), nil))
							return true
						}
						return false
					})
				}
				p.Sources = append(p.Sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
			}
			if fault != "strict" {
				p.Metadata = &migration.MetadataPolicy{DeviceLookup: "v2_cold_start", ArchiveUserTimestamps: true}
			}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			before := map[uint64]map[string][32]byte{}
			for _, n := range p.Sources {
				before[n.NodeID] = fileDigests(t, n.DataDir)
			}
			prepared, err := migration.Prepare(ctx, p, w, r, r, nil)
			for _, n := range p.Sources {
				require.Equal(t, before[n.NodeID], fileDigests(t, n.DataDir))
			}
			if fault != "archive" {
				require.Error(t, err)
				require.Contains(t, err.Error(), "record conflicts")
				if fault == "device-token" {
					require.Contains(t, err.Error(), "Device")
				} else {
					require.Contains(t, err.Error(), "User")
				}
				_, found, e := w.Get(ctx, []byte("workflow/PREPARED"))
				require.NoError(t, e)
				require.False(t, found)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, prepared.Selection.UserTimestamps)
			require.Equal(t, prepared.Capture.Tables["User"], prepared.Selection.UserTimestamps.Rows)
			require.Equal(t, prepared.Selection.UserTimestamps.Rows*2, prepared.Selection.UserTimestamps.Fields)
			require.Len(t, prepared.Selection.UserTimestamps.SHA256, 64)
			archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
			require.NoError(t, err)
			_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: p.Digest(), SourceCommit: p.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
			require.NoError(t, err)
			for _, n := range p.Sources {
				require.NoError(t, os.Rename(n.DataDir, n.DataDir+"-unmounted"))
			}
			fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
			require.NoError(t, err)
			defer fresh.Close()
			rebuilt, err := migration.PrepareArchive(ctx, p, fresh, r, archive)
			require.NoError(t, err)
			require.Equal(t, prepared.Selection.UserTimestamps, rebuilt.Selection.UserTimestamps)
			require.NoError(t, migrationv3.Install(ctx, p.Target, rebuilt.Conversion, fresh))
			verified, err := migration.VerifyTargets(ctx, p.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
			require.NoError(t, err)
			require.False(t, verified.CutoverReady)
		})
	}
}
