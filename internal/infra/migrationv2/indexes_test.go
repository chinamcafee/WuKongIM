package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"hash/fnv"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func TestPreflightRejectsOriginalBusinessIndexCorruption(t *testing.T) {
	tests := []struct {
		name     string
		table    uint16
		kind     byte
		index    uint16
		mutation string
	}{
		{"missing-device-uid", 0x0301, 3, 0x0301, "delete"},
		{"orphan-device-uid", 0x0301, 3, 0x0301, "orphan"},
		{"wrong-device-uid", 0x0301, 3, 0x0301, "wrong-key"},
		{"extra-device-uid-alias", 0x0301, 3, 0x0301, "duplicate"},
		{"invalid-device-index-value", 0x0301, 3, 0x0301, "wrong-value"},
		{"invalid-device-index-shape", 0x0301, 3, 0x0301, "wrong-shape"},
		{"invalid-device-index-type", 0x0301, 3, 0x0301, "wrong-type"},
		{"missing-subscriber-permission", 0x0401, 2, 0x0404, "delete"},
		{"missing-allowlist-permission", 0x0801, 2, 0x0808, "delete"},
		{"missing-denylist-permission", 0x0701, 2, 0x0701, "delete"},
		{"missing-conversation-existence", 0x0901, 2, 0x0901, "delete"},
		{"wrong-conversation-reference", 0x0901, 2, 0x0901, "wrong-value"},
		{"missing-message-id", 0x0101, 2, 0x0101, "delete"},
		{"wrong-message-id-reference", 0x0101, 2, 0x0101, "wrong-value"},
		{"mismatched-message-id-reference", 0x0101, 2, 0x0101, "mismatch"},
		{"extra-message-id-alias", 0x0101, 2, 0x0101, "duplicate"},
		{"malformed-administrative-channel-index", 0x0601, 2, 0x0601, "wrong-shape"},
		{"missing-client-message-number", 0x0101, 3, 0x0102, "delete"},
		{"missing-sender-unread-index", 0x0101, 3, 0x0101, "delete"},
		{"orphan-sender-unread-index", 0x0101, 3, 0x0101, "orphan"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := unpackNamedFixture(t, "original-v2-server.tar.gz")
			changed := false
			for shard := 0; shard < 2 && !changed; shard++ {
				db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", fmt.Sprintf("shard%03d", shard)), &pebble.Options{ErrorIfNotExists: true})
				require.NoError(t, err)
				it := db.NewIter(nil)
				var key, value []byte
				for ok := it.First(); ok; ok = it.Next() {
					k := it.Key()
					if len(k) >= 6 && binary.BigEndian.Uint16(k) == tc.table && k[2] == tc.kind && binary.BigEndian.Uint16(k[4:]) == tc.index {
						key = bytes.Clone(k)
						value = bytes.Clone(it.Value())
						break
					}
				}
				require.NoError(t, it.Close())
				if key != nil {
					changed = true
					b := db.NewBatch()
					switch tc.mutation {
					case "delete":
						require.NoError(t, b.Delete(key, nil))
					case "mismatch":
						binary.BigEndian.PutUint64(value[8:], 2)
						require.NoError(t, b.Set(key, value, nil))
					case "wrong-value":
						require.NoError(t, b.Set(key, []byte("incorrect-reference"), nil))
					case "orphan":
						key[len(key)-1] ^= 0x40
						require.NoError(t, b.Set(key, value, nil))
					case "duplicate":
						key[6] ^= 0x40
						require.NoError(t, b.Set(key, value, nil))
					case "wrong-key":
						require.NoError(t, b.Delete(key, nil))
						key[6] ^= 0x40
						require.NoError(t, b.Set(key, value, nil))
					case "wrong-shape":
						require.NoError(t, b.Set(append(key, 0), value, nil))
					case "wrong-type":
						key[4] = 0xff
						require.NoError(t, b.Set(key, value, nil))
					}
					require.NoError(t, b.Commit(pebble.Sync))
					require.NoError(t, b.Close())
				}
				require.NoError(t, db.Close())
			}
			require.True(t, changed, "the exact original fixture must contain the tested index")
			_, err := prepareIndexFixture(t, source)
			require.Error(t, err, "source index corruption must not become a successful migration")
			require.Contains(t, err.Error(), "index")
		})
	}
}

func prepareIndexFixture(t *testing.T, source string) (migration.Preflight, error) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	w, err := transfer.OpenSpool(filepath.Join(root, "spool"), "source-index-contract", 128<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Close()) })
	p := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "source-index-contract", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57881", DataDir: filepath.Join(root, "target")}}}}
	return migration.Prepare(ctx, p, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
}

// This test uses every healthy original-server source shape, without deriving
// expected indexes from a target converter or a v3 storage writer.
func TestSourceIndexesAcceptOriginalServerFixtures(t *testing.T) {
	for _, fixture := range []string{"original-v2-server.tar.gz", "original-v2-empty.tar.gz", "three-node"} {
		t.Run(fixture, func(t *testing.T) {
			ctx := context.Background()
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "original-index-positive", 128<<20)
			require.NoError(t, err)
			defer w.Close()
			var sources []migration.NodeOptions
			if fixture == "three-node" {
				for i := 1; i <= 3; i++ {
					sources = append(sources, migration.NodeOptions{NodeID: uint64(i), Options: migration.Options{DataDir: unpackNamedFixture(t, fmt.Sprintf("original-v2-three-%d.tar.gz", i)), ShardCount: 2}})
				}
			} else {
				sources = []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: unpackNamedFixture(t, fixture), ShardCount: 2}}}
			}
			reader := migrationv2.Reader{}
			capture, err := migration.CaptureSources(ctx, sources, reader, w, nil)
			require.NoError(t, err)
			_, err = migration.BuildSourceCatalog(ctx, capture, w, reader)
			require.NoError(t, err)
			require.NoError(t, migration.ValidateSourceIndexes(ctx, capture, sources, w, reader))
			require.NoError(t, migration.ValidateSourceIndexes(ctx, capture, sources, w, reader), "same-source retry must reproduce the index proof")
		})
	}
}

func TestSourceIndexesPreserveSparseAdministrativeRowsAndInertMessageResidue(t *testing.T) {
	for _, mode := range []string{"sparse-administrative", "retained-prefix"} {
		t.Run(mode, func(t *testing.T) {
			source := compatibleMessageFixture(t)
			changed := rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
				table, kind := binary.BigEndian.Uint16(key), key[2]
				if mode == "sparse-administrative" && (kind == 2 || kind == 3) {
					// These lookups are not used for account, channel/config identity or
					// ordinary conversation listing in the pinned source release.
					if table == 0x0201 || table == 0x0601 || table == 0x0b01 || (table == 0x0301 && binary.BigEndian.Uint16(key[4:]) != 0x0301) || (table == 0x0901 && kind == 3) {
						require.NoError(t, b.Delete(key, nil))
						return true
					}
				}
				if mode == "retained-prefix" && table == 0x0101 && kind == 1 && len(key) == 22 && binary.BigEndian.Uint64(key[12:20]) == 1 && bytes.Equal(key[4:12], originalChannelHashBytes("migrationgroup", 2)) {
					// Remove a retained prefix without touching original ID/client/sender
					// indexes. Later live sender sequences still define exactly the same max.
					require.NoError(t, b.Delete(key, nil))
					return true
				}
				return false
			})
			require.Positive(t, changed)
			_, err := prepareIndexFixture(t, source)
			if mode == "retained-prefix" {
				require.ErrorContains(t, err, "retained prefix")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestArchiveRebuildRejectsMissingSourceBusinessIndex(t *testing.T) {
	ctx := context.Background()
	source := unpackNamedFixture(t, "original-v2-server.tar.gz")
	require.Positive(t, rewriteOriginalIndexFixture(t, source, func(key, value []byte, b *pebble.Batch) bool {
		if binary.BigEndian.Uint16(key) == 0x0301 && key[2] == 3 && binary.BigEndian.Uint16(key[4:]) == 0x0301 {
			require.NoError(t, b.Delete(key, nil))
			return true
		}
		return false
	}))
	root := t.TempDir()
	w, err := transfer.OpenSpool(filepath.Join(root, "capture"), "archive-index-contract", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}}
	reader := migrationv2.Reader{}
	capture, err := migration.CaptureSources(ctx, plan.Sources, reader, w, nil)
	require.NoError(t, err)
	catalog, err := migration.BuildSourceCatalog(ctx, capture, w, reader)
	require.NoError(t, err)
	selected, err := migration.SelectSources(ctx, capture, catalog, w, reader, nil)
	require.NoError(t, err)
	archive, err := archivefs.NewFileArchiveStore(filepath.Join(root, "archive"))
	require.NoError(t, err)
	// A self-consistent archive produced before index preflight was introduced
	// still cannot bypass the new check through import or verify.
	_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, capture, catalog, selected, w, archive)
	require.NoError(t, err)
	fresh, err := transfer.OpenSpool(filepath.Join(root, "rebuild"), "archive-index-contract", 128<<20)
	require.NoError(t, err)
	defer fresh.Close()
	_, err = migration.PrepareArchive(ctx, plan, fresh, reader, archive)
	require.ErrorContains(t, err, "source business index is missing")
}

func TestSourceIndexRejectsMessageIDShadowAcrossShards(t *testing.T) {
	source := unpackNamedFixture(t, "original-v2-server.tar.gz")
	var key, value []byte
	var fromShard int
	require.NoError(t, migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: source, ShardCount: 2}, func(row migrationv2.Row) error {
		if key == nil && row.Table == "Message" && row.Kind == migrationv2.Index && len(row.Key) == 14 {
			key = bytes.Clone(row.Key)
			value = bytes.Clone(row.Value)
			fromShard = row.Shard
		}
		return nil
	}))
	require.NotEmpty(t, key)
	db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", fmt.Sprintf("shard%03d", 1-fromShard)), &pebble.Options{ErrorIfNotExists: true})
	require.NoError(t, err)
	require.NoError(t, db.Set(key, value, pebble.Sync))
	require.NoError(t, db.Close())
	_, err = prepareIndexFixture(t, source)
	require.ErrorContains(t, err, "source index conflict")
}

// rewriteOriginalIndexFixture edits only a freshly unpacked private source copy.
func rewriteOriginalIndexFixture(t *testing.T, source string, change func([]byte, []byte, *pebble.Batch) bool) int {
	t.Helper()
	changed := 0
	for shard := 0; shard < 2; shard++ {
		db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", fmt.Sprintf("shard%03d", shard)), &pebble.Options{ErrorIfNotExists: true})
		require.NoError(t, err)
		it := db.NewIter(nil)
		batch := db.NewBatch()
		for ok := it.First(); ok; ok = it.Next() {
			if change(bytes.Clone(it.Key()), bytes.Clone(it.Value()), batch) {
				changed++
			}
		}
		require.NoError(t, it.Error())
		require.NoError(t, it.Close())
		require.NoError(t, batch.Commit(pebble.Sync))
		require.NoError(t, batch.Close())
		require.NoError(t, db.Close())
	}
	return changed
}

func originalChannelHashBytes(id string, typ uint8) []byte {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("%s-%d", id, typ)))
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, h.Sum64())
	return b
}
