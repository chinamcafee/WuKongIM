package migrationv2_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/stretchr/testify/require"
)

func TestReadStoppedOriginalServerBindsTopologyAndRecoveryFiles(t *testing.T) {
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	before := fileDigests(t, dir)
	counts := map[string]int{}
	snapshot, err := migrationv2.ReadStoppedNode(context.Background(), migrationv2.NodeOptions{
		NodeID: 1, Options: migrationv2.Options{DataDir: dir, ShardCount: 2},
	}, func(row migrationv2.Row) error {
		if row.Kind == migrationv2.Primary {
			counts[row.Table]++
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.Equal(t, uint32(64), snapshot.Config.SlotCount)
	require.Len(t, snapshot.Config.Slots, 64)
	require.Equal(t, uint64(1), snapshot.Config.Slots[15].Leader)
	// Original source ignores db.slotShardNum in the product constructor; the
	// stopped inventory, rather than that unused setting, contains eight DBs.
	require.Equal(t, 8, snapshot.SlotShardCount)
	require.Equal(t, int64(0), snapshot.NotificationDepth)
	require.NotEmpty(t, snapshot.DataDigest)
	require.Equal(t, 4, counts["Message"])
	require.Equal(t, 2, counts["Device"])
	require.Equal(t, before, fileDigests(t, dir))
}

func TestStoppedOriginalServerIncludesNormallyRecoverableConversations(t *testing.T) {
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	actual := map[string]uint64{}
	_, err := migrationv2.ReadStoppedNode(context.Background(), migrationv2.NodeOptions{
		NodeID: 1, Options: migrationv2.Options{DataDir: dir, ShardCount: 2},
	}, func(row migrationv2.Row) error {
		if row.Table == "PendingConversation" {
			require.Equal(t, "migrationgroup____cmd", string(row.Fields["ChannelId"]))
			actual[string(row.Fields["Uid"])] = binary.BigEndian.Uint64(row.Fields["ReadedToMsgSeq"])
		}
		return nil
	}, nil)
	require.NoError(t, err)
	// These exact pending entries were written by the original server's normal
	// shutdown; its recovery uses AddConversationsIfNotExist, never overwrites.
	require.Equal(t, map[string]uint64{"migrationalice": 1, "migrationbob": 0}, actual)
}

func TestStoppedSlotProgressMatchesOriginalPublicAPI(t *testing.T) {
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	data, err := os.ReadFile("testdata/original-v2-server-slot-api.json")
	require.NoError(t, err)
	var expected map[string]struct {
		Last    uint64 `json:"last_log_seq"`
		Applied uint64 `json:"applied_index"`
	}
	require.NoError(t, json.Unmarshal(data, &expected))
	snapshot, err := migrationv2.ReadStoppedNode(context.Background(), migrationv2.NodeOptions{
		NodeID: 1, Options: migrationv2.Options{DataDir: dir, ShardCount: 2},
	}, func(migrationv2.Row) error { return nil }, nil)
	require.NoError(t, err)
	require.Len(t, snapshot.SlotProgress, 64)
	for _, actual := range snapshot.SlotProgress {
		want, ok := expected[actual.Group]
		require.True(t, ok)
		require.Equal(t, want.Last, actual.LastIndex, actual.Group)
		require.Equal(t, want.Applied, actual.AppliedIndex, actual.Group)
		if want.Last > 0 {
			require.NotEmpty(t, actual.LastDigest)
		}
	}
	require.Equal(t, snapshot.Config.Version, snapshot.ConfigProgress.AppliedIndex)
}
