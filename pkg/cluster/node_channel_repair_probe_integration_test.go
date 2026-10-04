//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestClusterChannelRepairProbeLoadsAndRefreshesDurableReplica(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	for _, n := range nodes {
		n.cfg.Channel.TickInterval = time.Hour
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	id := ch.ChannelID{ID: "repair-durable-cold-replica", Type: 1}
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	receipt, err := nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: 9101, Payload: []byte("cold-repair-proof")}})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := nodes[0].readChannelMigrationRuntimeMeta(ctx, route.HashSlot, id.ID, int64(id.Type))
	if err != nil {
		t.Fatal(err)
	}
	follower := firstNonLeaderNode(t, nodes, meta.Leader)
	requireChannelMessage(t, follower, id, receipt.MessageSeq, 9101, []byte("cold-repair-proof"))
	before, err := follower.ChannelRuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{id}})
	if err != nil || len(before.Missing) != 1 {
		t.Fatalf("cold before=%+v err=%v", before, err)
	}
	leader := clusterNodeByID(t, nodes, meta.Leader)
	proof, err := leader.ProbeChannel(ctx, follower.cfg.NodeID, id.ID, id.Type)
	if err != nil {
		t.Fatal(err)
	}
	if proof.ChannelID != id || proof.Role != ch.RoleFollower || proof.ChannelEpoch != meta.ChannelEpoch || proof.LeaderEpoch != meta.LeaderEpoch || proof.LEO < receipt.MessageSeq || proof.HW > proof.LEO {
		t.Fatalf("proof=%+v receipt=%+v meta=%+v", proof, receipt, meta)
	}

	// The loaded follower reactor does not consume quorum exchange writes.
	// Later migration probes must refresh its durable frontier independently.
	for _, messageID := range []uint64{9102, 9103} {
		receipt, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: messageID, Payload: []byte("after-probe")}})
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool {
		refreshed, err := leader.ProbeChannel(ctx, follower.cfg.NodeID, id.ID, id.Type)
		return err == nil && refreshed.LEO >= receipt.MessageSeq && refreshed.HW > proof.HW && refreshed.HW <= refreshed.LEO
	}, 3*time.Second, time.Millisecond)
}
