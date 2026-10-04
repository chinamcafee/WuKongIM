//go:build integration

package proxy

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCMDDeviceCursorReadsAuthoritativeRemoteSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)
	uid := findUIDForSlot(t, nodes[0].cluster, 2, "cmd-device")
	key := metadb.CMDDeviceCursorKey{UID: uid, DeviceFlag: 2, ChannelID: "c____cmd", ChannelType: 2}
	row := metadb.CMDDeviceCursor{UID: uid, DeviceFlag: 2, ChannelID: key.ChannelID, ChannelType: 2, ReadSeq: 99, UpdatedAt: 100}
	require.NoError(t, nodes[1].db.MetaDB().HashSlot(metadb.HashSlot(mustHashSlotForKey(t, nodes[1].cluster, uid))).UpsertCMDDeviceCursor(ctx, row))
	got, ok, err := nodes[0].store.GetCMDDeviceCursor(ctx, key)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, row, got)
	key.DeviceFlag = 0
	_, ok, err = nodes[0].store.GetCMDDeviceCursor(ctx, key)
	require.NoError(t, err)
	require.False(t, ok)
}
