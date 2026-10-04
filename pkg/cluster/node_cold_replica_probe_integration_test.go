//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// Quorum exchanges persist follower records without requiring a loaded reactor.
// Repair proof must remain available for that native, non-migration state.
func TestNativeQuorumColdFollowerRepairProbe(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	id := ch.ChannelID{ID: "native-cold-repair", Type: 2}
	waitRouteKeyLeaderConverged(t, nodes, id.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	meta := metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2, Status: uint8(ch.StatusActive), WriteFenceVersion: 1})
	require.NoError(t, (defaultChannelRuntimeMetaStore{node: nodes[0]}).UpsertChannelRuntimeMeta(ctx, meta))
	for index := uint64(1); index <= 2; index++ {
		_, err := nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 100 + index, FromUID: "native-sender", ClientMsgNo: string(rune('a' + index)), Payload: []byte("native payload")}})
		require.NoError(t, err)
	}
	var follower *Node
	// The successful native quorum must have at least one durable follower.
	require.Eventually(t, func() bool {
		for _, node := range nodes[1:] {
			store, err := node.defaultChannelStore.ChannelStore(ch.ChannelKeyForID(id), id)
			if err != nil {
				continue
			}
			_, found, readErr := store.(channelstore.MessageLookup).LookupMessageByID(ctx, 102)
			closeErr := store.Close()
			if readErr == nil && closeErr == nil && found {
				follower = node
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
	before, err := follower.ChannelRuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{id}})
	require.NoError(t, err)
	require.Empty(t, before.Channels, "the native follower must remain cold before the repair probe")
	proof, err := nodes[0].ProbeChannel(ctx, follower.NodeID(), id.ID, id.Type)
	require.NoError(t, err, "durable native replica must provide repair evidence even when cold")
	require.Equal(t, id, proof.ChannelID)
	require.Equal(t, ch.RoleFollower, proof.Role)
	require.Equal(t, ch.StatusActive, proof.Status)
	require.EqualValues(t, 1, proof.ChannelEpoch)
	require.GreaterOrEqual(t, proof.LEO, uint64(2))
	_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 103, FromUID: "native-sender", ClientMsgNo: "after-activation", Payload: []byte("native payload")}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		proof, err := nodes[0].ProbeChannel(ctx, follower.NodeID(), id.ID, id.Type)
		return err == nil && proof.LEO >= 3
	}, 2*time.Second, 10*time.Millisecond, "a loaded follower must expose subsequent native exchange durability")
}
