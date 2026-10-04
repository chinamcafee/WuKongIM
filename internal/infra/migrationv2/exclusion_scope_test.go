package migrationv2_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// The operator's only business exclusion is legacy Stream/StreamMeta. Exercise
// the whole preflight so that the option cannot bypass source identity checks
// or turn an incompatible channel/conversation into a successful preparation.
func TestLegacyStreamExclusionDoesNotBypassOriginalIdentityValidation(t *testing.T) {
	for _, tc := range []struct {
		name, table string
		column      uint16
		value       []byte
		want        string
	}{
		{"empty-channel-id", "ChannelInfo", 0x0602, []byte{}, "invalid channel identity"},
		{"zero-channel-type", "ChannelInfo", 0x0603, []byte{0}, "invalid channel identity"},
		{"changed-config-type-key-mismatch", "ChannelClusterConfig", 0x0b02, []byte{0}, "channel identity does not match its key"},
		{"zero-config-term", "ChannelClusterConfig", 0x0b07, make([]byte, 4), "channel authority is not initialized"},
		{"opaque-config-key-mismatch", "ChannelClusterConfig", 0x0b01, []byte{0xff, 0xfe}, "channel identity does not match its key"},
		{"opaque-conversation-owner-mismatch", "Conversation", 0x0901, []byte{0xff, 0xfe}, "conversation UID does not match its key"},
		{"opaque-conversation-index-mismatch", "Conversation", 0x0902, []byte{0xff, 0xfe}, "source business index is missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := unpackNamedFixture(t, "original-v2-server.tar.gz")
			var primary migration.Row
			require.NoError(t, migrationv2.Scan(context.Background(), migration.Options{DataDir: source, ShardCount: 2}, func(r migration.Row) error {
				if r.Table == tc.table && r.Kind == migration.Primary && primary.Key == nil {
					primary = r
				}
				return nil
			}))
			require.NotEmpty(t, primary.Key)
			db, err := pebble.Open(filepath.Join(source, "db", "wukongimdb", fmt.Sprintf("shard%03d", primary.Shard)), &pebble.Options{ErrorIfNotExists: true})
			require.NoError(t, err)
			key := make([]byte, len(primary.Key)+2)
			copy(key, primary.Key)
			binary.BigEndian.PutUint16(key[len(primary.Key):], tc.column)
			require.NoError(t, db.Set(key, tc.value, pebble.Sync))
			require.NoError(t, db.Close())
			before := fileDigests(t, source)
			root := t.TempDir()
			target := filepath.Join(root, "target")
			plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}, Exclusions: &migration.Exclusions{LegacyStreamStorage: true}, Target: migration.TargetPlan{ClusterID: "stream-only-exclusion", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57881", DataDir: target}}}}
			w, err := transfer.OpenSpool(filepath.Join(root, "spool"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			reader := migrationv2.Reader{}
			result, err := migration.Prepare(context.Background(), plan, w, reader, reader, nil)
			require.ErrorContains(t, err, tc.want)
			require.Empty(t, result.Status)
			require.False(t, result.CutoverReady)
			_, complete, err := w.Get(context.Background(), []byte("workflow/PREPARED"))
			require.NoError(t, err)
			require.False(t, complete)
			require.NoDirExists(t, target)
			require.Equal(t, before, fileDigests(t, source))
		})
	}
}
