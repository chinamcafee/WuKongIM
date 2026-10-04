package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"testing"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// addEmptyChannelPair writes a private original-format administrative pair and
// its retained applied Slot command. No existing business record is removed.
func addEmptyChannelPair(t *testing.T, source string, node uint64, mode string) {
	t.Helper()
	var cfg, info migration.Row
	snap, err := migrationv2.ReadStoppedNode(context.Background(), migration.NodeOptions{NodeID: node, Options: migration.Options{DataDir: source, ShardCount: 2}}, func(r migration.Row) error {
		if r.Kind == migration.Primary && string(r.Fields["ChannelId"]) == "migrationgroup" {
			if r.Table == "ChannelInfo" {
				info = r
			}
			if r.Table == "ChannelClusterConfig" {
				cfg = r
			}
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, cfg.Fields)
	require.NotEmpty(t, info.Fields)
	var index uint64
	for _, p := range snap.SlotProgress {
		if p.Group == "0" {
			index = p.LastIndex + 1
		}
	}
	require.Positive(t, index)
	owner := counterHash("-0")
	u64 := func(n uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, n); return b }
	cfg.Fields["ChannelId"], cfg.Fields["ChannelType"] = []byte{}, []byte{0}
	cfg.Fields["ConfVersion"] = u64(index)
	cfg.Fields["MigrateFrom"], cfg.Fields["MigrateTo"], cfg.Fields["Learners"] = u64(0), u64(0), []byte{}
	cfg.Fields["Status"] = []byte{0}
	info.Fields["ChannelId"], info.Fields["ChannelType"] = []byte{}, []byte{0}
	for name, v := range info.Fields {
		if name != "CreatedAt" && name != "UpdatedAt" && name != "ChannelId" && name != "ChannelType" {
			info.Fields[name] = make([]byte, len(v))
		}
	}
	if mode == "policy" {
		info.Fields["Ban"] = []byte{1}
	}
	for _, r := range []migration.Row{info, cfg} {
		if mode == "missing_body" && r.Table == "ChannelInfo" {
			continue
		}
		cols := map[string]uint16{"ChannelId": 0x0602, "ChannelType": 0x0603, "Ban": 0x0604, "Large": 0x0605, "Disband": 0x0606, "SubscriberCount": 0x0607, "AllowlistCount": 0x0608, "DenylistCount": 0x0609, "CreatedAt": 0x060a, "UpdatedAt": 0x060b, "SendBan": 0x060c, "AllowStranger": 0x060d}
		table, shard := uint16(0x0601), int(owner%2)
		if r.Table == "ChannelClusterConfig" {
			table, shard = 0x0b01, 0
			cols = map[string]uint16{"ChannelId": 0x0b01, "ChannelType": 0x0b02, "ReplicaMaxCount": 0x0b03, "Replicas": 0x0b04, "Learners": 0x0b05, "LeaderId": 0x0b06, "Term": 0x0b07, "MigrateFrom": 0x0b08, "MigrateTo": 0x0b09, "Status": 0x0b0a, "ConfVersion": 0x0b0b, "Version": 0x0b0c, "CreatedAt": 0x0b0d, "UpdatedAt": 0x0b0e}
		}
		db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", fmt.Sprintf("shard%03d", shard)), &pebble.Options{ErrorIfNotExists: true})
		require.NoError(t, err)
		b := db.NewBatch()
		for name, v := range r.Fields {
			col, ok := cols[name]
			require.True(t, ok, name)
			k := make([]byte, 14)
			binary.BigEndian.PutUint16(k, table)
			k[2] = 1
			binary.BigEndian.PutUint64(k[4:], owner)
			binary.BigEndian.PutUint16(k[12:], col)
			require.NoError(t, b.Set(k, v, nil))
		}
		if mode == "dangling_permission_index" && r.Table == "ChannelInfo" {
			k := []byte{7, 1, 2, 0, 7, 1}
			k = append(k, u64(owner)...)
			k = append(k, u64(123)...)
			require.NoError(t, b.Set(k, u64(123), nil))
		}
		require.NoError(t, b.Commit(pebble.Sync))
		require.NoError(t, b.Close())
		require.NoError(t, db.Close())
	}
	if mode == "missing_command" {
		return
	}
	var buffer bytes.Buffer
	write := func(v any) { require.NoError(t, binary.Write(&buffer, binary.BigEndian, v)) }
	str := func(v []byte) { write(uint16(len(v))); buffer.Write(v) }
	write(uint16(1))
	write(uint16(28))
	str(nil)
	write(uint8(0))
	write(uint16(1))
	str(nil)
	write(uint8(0))
	buffer.Write(cfg.Fields["ReplicaMaxCount"])
	for _, name := range []string{"Replicas", "Learners"} {
		v := cfg.Fields[name]
		write(uint16(len(v) / 8))
		buffer.Write(v)
	}
	for _, name := range []string{"LeaderId", "Term", "MigrateFrom", "MigrateTo", "Status", "ConfVersion", "CreatedAt", "UpdatedAt"} {
		v := cfg.Fields[name]
		if len(v) == 0 {
			v = u64(0)
		}
		buffer.Write(v)
	}
	raw := buffer.Bytes()
	if mode == "trailing_command" {
		raw = append(raw, 1)
	}
	h := fnv.New32()
	h.Write([]byte("0"))
	shard := h.Sum32() % uint32(snap.SlotShardCount)
	db, err := pebble.Open(filepath.Join(source, "cluster", "logdb", fmt.Sprintf("shard%03d", shard)), &pebble.Options{ErrorIfNotExists: true})
	require.NoError(t, err)
	batch := db.NewBatch()
	it := db.NewIter(nil)
	var prior []byte
	hash := counterHash("0")
	for ok := it.First(); ok; ok = it.Next() {
		k, v := bytes.Clone(it.Key()), bytes.Clone(it.Value())
		if len(k) < 12 || binary.BigEndian.Uint64(k[4:12]) != hash {
			continue
		}
		switch binary.BigEndian.Uint16(k) {
		case 0x0101:
			if binary.BigEndian.Uint64(k[12:]) == index-1 {
				prior = v
			}
		case 0x0202, 0x0303:
			binary.BigEndian.PutUint64(v, index)
			require.NoError(t, batch.Set(k, v, nil))
		}
	}
	require.NoError(t, it.Close())
	if len(prior) == 0 {
		prior = make([]byte, 28)
		binary.BigEndian.PutUint32(prior[16:], 1)
	}
	for _, table := range []uint16{0x0202, 0x0303} {
		k := make([]byte, 12)
		binary.BigEndian.PutUint16(k, table)
		binary.BigEndian.PutUint64(k[4:], hash)
		v := make([]byte, 16)
		binary.BigEndian.PutUint64(v, index)
		require.NoError(t, batch.Set(k, v, nil))
	}
	key := make([]byte, 20)
	key[0], key[1] = 1, 1
	binary.BigEndian.PutUint64(key[4:], hash)
	binary.BigEndian.PutUint64(key[12:], index)
	value := bytes.Clone(prior[:20])
	binary.BigEndian.PutUint64(value, index)
	binary.BigEndian.PutUint64(value[8:], index)
	value = append(value, raw...)
	value = append(value, prior[len(prior)-8:]...)
	require.NoError(t, batch.Set(key, value, nil))
	require.NoError(t, batch.Commit(pebble.Sync))
	require.NoError(t, batch.Close())
	require.NoError(t, db.Close())
}

func TestEmptyChannelArchiveRequiresProofAndRebuildsWithoutSources(t *testing.T) {
	for _, nodes := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d_node_cluster", nodes), func(t *testing.T) {
			ctx := context.Background()
			r := migrationv2.Reader{}
			var sources []migration.NodeOptions
			for n := 1; n <= nodes; n++ {
				name := "original-v2-server.tar.gz"
				if nodes > 1 {
					name = fmt.Sprintf("original-v2-three-%d.tar.gz", n)
				}
				dir := unpackNamedFixture(t, name)
				clearFixtureMessageExtensions(t, dir)
				addEmptyChannelPair(t, dir, uint64(n), "")
				sources = append(sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
			}
			plan := diagnosticPlan(t, sources[0].DataDir)
			plan.Sources = sources
			open := func() *transfer.Spool {
				w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, w.Close()) })
				return w
			}
			_, err := migration.Prepare(ctx, plan, open(), r, r, nil)
			require.ErrorContains(t, err, "invalid channel identity")
			plan.Metadata = conversationPolicy()
			plan.Metadata.ArchiveEmptyChannels = true
			w := open()
			p, err := migration.Prepare(ctx, plan, w, r, r, nil)
			require.NoError(t, err)
			require.EqualValues(t, nodes*2, p.Selection.EmptyChannels.Rows)
			require.EqualValues(t, nodes, p.Selection.EmptyChannels.Commands)
			require.EqualValues(t, nodes*2, p.Selection.Preserved["unreferenced_empty_channel_administration"])
			require.NoError(t, migration.WalkSelectedSources(ctx, w, func(rec migration.SelectedRecord) error {
				if rec.Row.Table == "ChannelInfo" || rec.Row.Table == "ChannelClusterConfig" {
					require.NotEmpty(t, rec.Identity.Channel.ID)
				}
				return nil
			}))
			archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
			require.NoError(t, err)
			_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, p.Capture, p.Catalog, p.Selection, w, archive)
			require.NoError(t, err)
			for _, n := range sources {
				require.NoError(t, os.Rename(n.DataDir, n.DataDir+"-unmounted"))
			}
			rebuilt, err := migration.PrepareArchive(ctx, plan, open(), r, archive)
			require.NoError(t, err)
			require.Equal(t, p.Selection, rebuilt.Selection)
		})
	}
}

func TestEmptyChannelArchiveRejectsBusinessAndMissingProof(t *testing.T) {
	for _, mode := range []string{"policy", "missing_body", "dangling_permission_index", "missing_command", "trailing_command"} {
		t.Run(mode, func(t *testing.T) {
			dir := compatibleMessageFixture(t)
			addEmptyChannelPair(t, dir, 1, mode)
			plan := diagnosticPlan(t, dir)
			plan.Metadata = conversationPolicy()
			plan.Metadata.ArchiveEmptyChannels = true
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			p, err := migration.Prepare(context.Background(), plan, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
			require.Error(t, err)
			require.Empty(t, p.Selection.Digest)
			require.False(t, p.CutoverReady)
		})
	}
}
