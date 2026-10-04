package migrationv2_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

func TestScanOriginalV2PreservesRowsAndDoesNotWriteSource(t *testing.T) {
	dir := unpackFixture(t)
	before := fileDigests(t, dir)
	var rows []migrationv2.Row
	err := migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: dir, ShardCount: 2}, func(row migrationv2.Row) error {
		rows = append(rows, row)
		return nil
	})
	require.NoError(t, err)
	// The literal identities are also present in the independently produced
	// expected.json from original v2 GetUser/GetDevice/LoadLastMsgs.
	counts := map[string]int{}
	for _, row := range rows {
		if row.Kind != migrationv2.Primary {
			continue
		}
		counts[row.Table]++
		switch row.Table {
		case "User":
			require.Equal(t, "用户:一", string(row.Fields["Uid"]))
			require.Equal(t, uint64(7976039811237385143), row.ID)
		case "Device":
			require.Equal(t, uint64(123456789), row.ID)
			require.Equal(t, "fixture-only-凭据", string(row.Fields["Token"]))
		case "Message":
			require.Equal(t, uint64(257), row.ID)
			require.Equal(t, uint64(17930037264876462710), row.Owner)
			require.Equal(t, uint64(9007199254740999), binary.BigEndian.Uint64(row.Fields["MessageId"]))
			require.Equal(t, uint32(3600), binary.BigEndian.Uint32(row.Fields["Expire"]))
			require.Equal(t, "群:一-2", string(row.Fields["ChannelId"]))
			require.Equal(t, "topic-一", string(row.Fields["Topic"]))
			require.Equal(t, "stream-一", string(row.Fields["StreamNo"]))
			require.Equal(t, []byte{0, 1, 255, 0}, row.Fields["Payload"])
		}
	}
	for _, table := range []string{"User", "Device", "Message", "ChannelInfo", "Subscriber", "Denylist", "Allowlist", "Conversation", "ChannelClusterConfig", "SystemUid"} {
		require.Equal(t, 1, counts[table], table)
	}
	require.Equal(t, before, fileDigests(t, dir))
}

func TestScanRejectsIncompleteMessageIdentity(t *testing.T) {
	dir := unpackFixture(t)
	var message migrationv2.Row
	require.NoError(t, migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: dir, ShardCount: 2}, func(row migrationv2.Row) error {
		if row.Table == "Message" && row.Kind == migrationv2.Primary {
			message = row
		}
		return nil
	}))
	// Deliberately corrupt only the private fixture copy, using the original
	// engine. Removing a primary ID must never become a successful partial scan.
	db, err := pebble.Open(filepath.Join(dir, "db", "wukongimdb", "shard000"), &pebble.Options{})
	require.NoError(t, err)
	require.NoError(t, db.Delete(append(message.Key, 0x01, 0x04), pebble.Sync))
	require.NoError(t, db.Close())
	err = migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: dir, ShardCount: 2}, func(migrationv2.Row) error { return nil })
	require.ErrorContains(t, err, "Message: missing MessageId")
}

func TestScanRejectsNestedReaderBeforeOpeningAnySourceLock(t *testing.T) {
	dir := unpackFixture(t)
	opts := migrationv2.Options{DataDir: dir, ShardCount: 2}
	checked := false
	require.NoError(t, migrationv2.Scan(context.Background(), opts, func(migrationv2.Row) error {
		if !checked {
			checked = true
			err := migrationv2.Scan(context.Background(), opts, func(migrationv2.Row) error { return nil })
			require.ErrorContains(t, err, "source scan already active")
		}
		return nil
	}))
	require.True(t, checked)
	require.NoError(t, migrationv2.Scan(context.Background(), opts, func(migrationv2.Row) error { return nil }))
}

func unpackFixture(t *testing.T) string {
	return unpackNamedFixture(t, "original-v2.tar.gz")
}

func unpackNamedFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	f, err := os.Open(filepath.Join("testdata", name))
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		require.True(t, filepath.IsLocal(h.Name))
		path := filepath.Join(dir, h.Name)
		if h.Typeflag == tar.TypeDir {
			require.NoError(t, os.MkdirAll(path, 0700))
			continue
		}
		require.Equal(t, byte(tar.TypeReg), h.Typeflag)
		b, err := io.ReadAll(tr)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, b, 0600))
	}
	return dir
}

func fileDigests(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	result := map[string][32]byte{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		result[rel] = sha256.Sum256(b)
		return err
	}))
	return result
}
