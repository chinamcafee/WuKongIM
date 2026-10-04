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

// addDuplicateDevice modifies a private fixture using its original column and
// UID-index layout. The later ID deliberately carries different credentials.
func addDuplicateDevice(t *testing.T, source string, node uint64) migration.Row {
	t.Helper()
	var original migration.Row
	_, err := migrationv2.ReadStoppedNode(context.Background(), migration.NodeOptions{NodeID: node, Options: migration.Options{DataDir: source, ShardCount: 2}}, func(row migration.Row) error {
		if row.Table == "Device" && row.Kind == migration.Primary && original.ID == 0 {
			original = row
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.NotZero(t, original.ID)
	later := original.ID + (1 << 20)
	changed := rewriteOriginalIndexFixture(t, source, func(k, v []byte, b *pebble.Batch) bool {
		if len(k) == 14 && bytes.Equal(k[:12], original.Key) {
			binary.BigEndian.PutUint64(k[4:], later)
			switch binary.BigEndian.Uint16(k[12:]) {
			case 0x0302:
				v = []byte("synthetic-hot-cache-token")
			case 0x0304:
				v = []byte{original.Fields["DeviceLevel"][0] ^ 1}
			}
			require.NoError(t, b.Set(k, v, nil))
			return true
		}
		if len(k) == 22 && bytes.Equal(k[:6], []byte{3, 1, byte(migration.SecondaryIndex), 0, 3, 1}) && binary.BigEndian.Uint64(k[14:]) == original.ID {
			binary.BigEndian.PutUint64(k[14:], later)
			require.NoError(t, b.Set(k, v, nil))
			return true
		}
		return false
	})
	require.Greater(t, changed, 1)
	return original
}

func TestColdDeviceLookupRebuildsFromArchiveAndVerifiesNativeTargets(t *testing.T) {
	for _, tc := range []struct{ sources, targets int }{{1, 1}, {3, 1}, {3, 3}} {
		t.Run(fmt.Sprintf("%d_to_%d_node_cluster", tc.sources, tc.targets), func(t *testing.T) {
			ctx := context.Background()
			r := migrationv2.Reader{}
			var sources []migration.NodeOptions
			var expected migration.Row
			for n := 1; n <= tc.sources; n++ {
				name := "original-v2-server.tar.gz"
				if tc.sources > 1 {
					name = fmt.Sprintf("original-v2-three-%d.tar.gz", n)
				}
				dir := unpackNamedFixture(t, name)
				clearFixtureMessageExtensions(t, dir)
				expected = addDuplicateDevice(t, dir, uint64(n))
				sources = append(sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
			}
			plan := diagnosticPlan(t, sources[0].DataDir)
			plan.Sources = sources
			plan.Target.Replicas, plan.Target.ChannelReplicas = uint16(tc.targets), uint16(tc.targets)
			for n := 2; n <= tc.targets; n++ {
				plan.Target.Nodes = append(plan.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + n), Addr: fmt.Sprintf("127.0.0.1:%d", 57900+n), DataDir: filepath.Join(t.TempDir(), "target")})
			}
			open := func(p migration.Plan) *transfer.Spool {
				w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, w.Close()) })
				return w
			}
			_, err := migration.Prepare(ctx, plan, open(plan), r, r, nil)
			require.Error(t, err, "the default must reject ambiguous credentials")
			plan.Metadata = &migration.MetadataPolicy{DeviceLookup: "v2_cold_start"}
			w := open(plan)
			p, err := migration.Prepare(ctx, plan, w, r, r, nil)
			require.NoError(t, err)
			require.EqualValues(t, tc.sources, p.Selection.Metadata.DuplicateGroups)
			require.EqualValues(t, tc.sources, p.Selection.Metadata.ShadowedRows)
			var found int
			require.NoError(t, migration.WalkSelectedSources(ctx, w, func(rec migration.SelectedRecord) error {
				if rec.Row.Table == "Device" && rec.Row.ID == expected.ID {
					found++
					require.Equal(t, expected.Fields, rec.Row.Fields, "token, level and timestamps must come from the exact cold-read primary")
				}
				return nil
			}))
			require.Equal(t, 1, found)
			archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
			require.NoError(t, err)
			_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, p.Capture, p.Catalog, p.Selection, w, archive)
			require.NoError(t, err)
			for _, source := range sources {
				require.NoError(t, os.Rename(source.DataDir, source.DataDir+"-unmounted"))
			}
			fresh := open(plan)
			rebuilt, err := migration.PrepareArchive(ctx, plan, fresh, r, archive)
			require.NoError(t, err)
			require.Equal(t, p.Selection, rebuilt.Selection)
			require.NoError(t, migrationv3.Install(ctx, plan.Target, rebuilt.Conversion, fresh))
			verified, err := migration.VerifyTargets(ctx, plan.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
			require.NoError(t, err)
			require.Equal(t, "offline_verified", verified.Status)
		})
	}
}

func TestColdDeviceLookupRejectsDamagedOriginalIndexes(t *testing.T) {
	for _, mode := range []string{"missing", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			dir := compatibleMessageFixture(t)
			original := addDuplicateDevice(t, dir, 1)
			changed := rewriteOriginalIndexFixture(t, dir, func(k, v []byte, b *pebble.Batch) bool {
				if len(k) != 22 || !bytes.Equal(k[:6], []byte{3, 1, byte(migration.SecondaryIndex), 0, 3, 1}) || binary.BigEndian.Uint64(k[14:]) != original.ID {
					return false
				}
				if mode == "missing" {
					require.NoError(t, b.Delete(k, nil))
				} else {
					binary.BigEndian.PutUint64(k[14:], original.ID-1)
					require.NoError(t, b.Set(k, v, nil))
				}
				return true
			})
			require.Equal(t, 1, changed)
			plan := diagnosticPlan(t, dir)
			plan.Metadata = &migration.MetadataPolicy{DeviceLookup: "v2_cold_start"}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			p, err := migration.Prepare(context.Background(), plan, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
			require.ErrorContains(t, err, "index")
			require.Empty(t, p.Selection.Digest)
		})
	}
}

func TestColdDeviceLookupStillRejectsConflictingAuthorityCredentials(t *testing.T) {
	var sources []migration.NodeOptions
	for n := 1; n <= 3; n++ {
		dir := unpackNamedFixture(t, fmt.Sprintf("original-v2-three-%d.tar.gz", n))
		clearFixtureMessageExtensions(t, dir)
		original := addDuplicateDevice(t, dir, uint64(n))
		if n == 2 {
			require.Equal(t, 1, rewriteOriginalIndexFixture(t, dir, func(k, v []byte, b *pebble.Batch) bool {
				if len(k) != 14 || !bytes.Equal(k[:12], original.Key) || binary.BigEndian.Uint16(k[12:]) != 0x0302 {
					return false
				}
				require.NoError(t, b.Set(k, []byte("synthetic-divergent-cold-token"), nil))
				return true
			}))
		}
		sources = append(sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
	}
	plan := diagnosticPlan(t, sources[0].DataDir)
	plan.Sources = sources
	plan.Metadata = &migration.MetadataPolicy{DeviceLookup: "v2_cold_start"}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	p, err := migration.Prepare(context.Background(), plan, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
	require.ErrorContains(t, err, "Device record conflicts between replica nodes")
	require.Empty(t, p.Selection.Digest)
}

func TestColdDeviceLookupPolicyRejectsUnknownOrImplicitRules(t *testing.T) {
	for _, rule := range []string{"", "latest", "max_updated_at"} {
		plan := diagnosticPlan(t, t.TempDir())
		plan.Metadata = &migration.MetadataPolicy{DeviceLookup: rule}
		_, err := migration.Prepare(context.Background(), plan, nil, nil, nil, nil)
		require.ErrorContains(t, err, "metadata.device_lookup")
	}
}
