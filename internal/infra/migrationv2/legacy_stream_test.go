package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
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

// These synthetic rows follow the removed original writer at 9fc693d5068b:
// Stream.Encode, StreamMeta.Encode and NewStream{Index,Meta}Key. No production
// payload, identity or credential is included in this fixture.
func legacyStreamFixture(t *testing.T) (string, []migrationv2.Row) {
	t.Helper()
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	const name = "synthetic-legacy-stream"
	var parent migrationv2.Row
	require.NoError(t, migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: dir, ShardCount: 2}, func(row migrationv2.Row) error {
		if row.Table == "Message" && row.Kind == migration.Primary && row.ID == 2 {
			parent = row
		}
		return nil
	}))
	require.NotEmpty(t, parent.Key)
	h := fnv.New64a()
	h.Write([]byte(name))
	streamHash := h.Sum64()
	s := fnv.New32()
	s.Write([]byte(name))
	shard := int(s.Sum32() % 2)
	chunkKey := make([]byte, 22)
	copy(chunkKey, []byte{0x11, 1, 2, 0, 0x11, 1})
	binary.BigEndian.PutUint64(chunkKey[6:], streamHash)
	binary.BigEndian.PutUint64(chunkKey[14:], 1)
	metaKey := make([]byte, 12)
	copy(metaKey, []byte{0x12, 1, 1, 0})
	binary.BigEndian.PutUint64(metaKey[4:], streamHash)
	var chunk, metadata bytes.Buffer
	str := func(b *bytes.Buffer, v string) {
		require.NoError(t, binary.Write(b, binary.BigEndian, int16(len(v))))
		b.WriteString(v)
	}
	require.NoError(t, binary.Write(&chunk, binary.BigEndian, int16(0)))
	require.NoError(t, binary.Write(&chunk, binary.BigEndian, uint64(1)))
	str(&chunk, name)
	chunk.Write([]byte{0, 255, 1, 2})
	require.NoError(t, binary.Write(&metadata, binary.BigEndian, int16(0)))
	str(&metadata, name)
	str(&metadata, string(parent.Fields["ChannelId"]))
	metadata.Write(parent.Fields["ChannelType"])
	str(&metadata, string(parent.Fields["FromUid"]))
	str(&metadata, string(parent.Fields["ClientMsgNo"]))
	metadata.Write(parent.Fields["MessageId"])
	metadata.Write(parent.Fields["MessageSeq"])
	rows := []migrationv2.Row{{Shard: shard, Table: "LegacyStream", Kind: migration.Index, Key: chunkKey, Value: chunk.Bytes()}, {Shard: shard, Table: "LegacyStreamMeta", Kind: migration.Primary, Key: metaKey, Value: metadata.Bytes()}}
	for i := 0; i < 2; i++ {
		db, err := pebble.Open(filepath.Join(dir, "db", "wukongimdb", fmt.Sprintf("shard%03d", i)), &pebble.Options{})
		require.NoError(t, err)
		for _, row := range rows {
			if row.Shard == i {
				require.NoError(t, db.Set(row.Key, row.Value, pebble.Sync))
			}
		}
		if parent.Shard == i {
			require.NoError(t, db.Set(append(bytes.Clone(parent.Key), 1, 14), []byte(name), pebble.Sync))
		}
		require.NoError(t, db.Close())
	}
	// Long-lived backups can also retain an empty-UID cache no-op. Include it
	// in the portable replay to verify its full provenance survives the archive.
	path := filepath.Join(dir, "conversationv2", "conversations.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var updates []json.RawMessage
	require.NoError(t, json.Unmarshal(data, &updates))
	updates = append(updates, json.RawMessage(`{"channel_id":"migrationgroup","channel_type":2,"user_read_seqs":{"":0},"tag_key":"synthetic-cache","last_msg_seq":3}`))
	data, err = json.Marshal(updates)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	return dir, rows
}

func TestLegacyStreamExclusionArchivesOnlyOldTablesAndStillChecksMainMessages(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, wantRows := legacyStreamFixture(t)
	before := fileDigests(t, source)
	w, err := transfer.OpenSpool(filepath.Join(root, "spool"), "stream-contract", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	capture, err := migration.CaptureSources(ctx, []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}, r, w, nil)
	require.NoError(t, err)
	catalog, err := migration.BuildSourceCatalog(ctx, capture, w, r)
	require.NoError(t, err)
	_, err = migration.SelectSources(ctx, capture, catalog, w, r, nil)
	require.ErrorContains(t, err, "legacy_stream_storage")
	selected, err := migration.SelectSources(ctx, capture, catalog, w, r, &migration.Exclusions{LegacyStreamStorage: true})
	require.NoError(t, err)
	var parents int
	require.NoError(t, migration.WalkSelectedSources(ctx, w, func(row migration.SelectedRecord) error {
		if row.Row.Table == "Message" && row.Row.Kind == migration.Primary {
			parents++
		}
		return nil
	}))
	require.Equal(t, 4, parents, "the exclusion never removes parent Message rows")
	_, err = migration.BuildTargetRecords(ctx, selected, w, r)
	require.ErrorContains(t, err, "incompatible with existing v3")
	_, found, err := w.Get(ctx, []byte("conversion/COMPLETE"))
	require.NoError(t, err)
	require.False(t, found)
	archive, err := archivefs.NewFileArchiveStore(filepath.Join(root, "diagnostic-archive"))
	require.NoError(t, err)
	manifest, err := migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: "stream-contract", SourceCommit: migrationv2.SourceCommit, ChunkBytes: 4096}, capture, catalog, selected, w, archive)
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Chunks)
	_, err = migration.ReadSourceArchive(ctx, archive, func(transfer.SpoolRow) error { return nil })
	require.NoError(t, err)
	var scanned []migrationv2.Row
	require.NoError(t, migrationv2.Scan(ctx, migrationv2.Options{DataDir: source, ShardCount: 2}, func(row migrationv2.Row) error {
		if row.Table == "LegacyStream" || row.Table == "LegacyStreamMeta" {
			scanned = append(scanned, row)
		}
		return nil
	}))
	require.ElementsMatch(t, wantRows, scanned)
	require.Equal(t, before, fileDigests(t, source))
}

func TestLegacyStreamExclusionDoesNotAcceptMalformedRecords(t *testing.T) {
	_, rows := legacyStreamFixture(t)
	for _, row := range rows {
		_, err := migrationv2.Identify(row)
		require.NoError(t, err)
		_, err = (migrationv2.Reader{}).DescribeIndexes(row, migration.RecordIdentity{}, 2)
		require.NoError(t, err)
	}
	cases := []struct {
		name   string
		index  int
		change func(*migrationv2.Row)
	}{
		{"unknown-version", 0, func(r *migrationv2.Row) { r.Value[1] = 1 }},
		{"truncated-version", 0, func(r *migrationv2.Row) { r.Value = r.Value[:1] }},
		{"negative-string-size", 0, func(r *migrationv2.Row) { r.Value[10] = 255; r.Value[11] = 255 }},
		{"oversized-string-size", 0, func(r *migrationv2.Row) { r.Value[10] = 127; r.Value[11] = 255 }},
		{"chunk-hash-mismatch", 0, func(r *migrationv2.Row) { r.Key[6] ^= 1 }},
		{"chunk-id-mismatch", 0, func(r *migrationv2.Row) { r.Key[21] ^= 1 }},
		{"wrong-key-kind", 0, func(r *migrationv2.Row) { r.Kind = migration.Primary; r.Key[2] = 1 }},
		{"wrong-index-name", 0, func(r *migrationv2.Row) { r.Key[5] = 2 }},
		{"short-chunk-key", 0, func(r *migrationv2.Row) { r.Key = r.Key[:21] }},
		{"invalid-utf8", 0, func(r *migrationv2.Row) { r.Value[12] = 255 }},
		{"truncated-meta", 1, func(r *migrationv2.Row) { r.Value = r.Value[:len(r.Value)-1] }},
		{"trailing-meta", 1, func(r *migrationv2.Row) { r.Value = append(r.Value, 1) }},
		{"metadata-hash-mismatch", 1, func(r *migrationv2.Row) { r.Key[4] ^= 1 }},
		{"unexpected-columns", 1, func(r *migrationv2.Row) { r.Fields = map[string][]byte{"Payload": []byte("synthetic-private")} }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := rows[tt.index]
			r.Key = bytes.Clone(r.Key)
			r.Value = bytes.Clone(r.Value)
			tt.change(&r)
			_, err := migrationv2.Identify(r)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "synthetic-private")
			_, err = (migrationv2.Reader{}).DescribeIndexes(r, migration.RecordIdentity{}, 2)
			require.Error(t, err)
		})
	}
	r := rows[0]
	r.Shard = 1 - r.Shard
	_, err := (migrationv2.Reader{}).DescribeIndexes(r, migration.RecordIdentity{}, 2)
	require.ErrorContains(t, err, "wrong original shard")
}
