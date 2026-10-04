//go:build integration

package migrationv2_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNativeV3EmptyReplicaControl uses only normal v3 writes. This file also
// runs alone against the pristine pre-migration revision to separate runtime
// recovery behavior from offline migration initialization.
func TestNativeV3EmptyReplicaControl(t *testing.T) { runNativeV3ReplicaControl(t, true) }

func TestNativeV3IntactReplicaControl(t *testing.T) { runNativeV3ReplicaControl(t, false) }

func runNativeV3ReplicaControl(t *testing.T, empty bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	var voters []cluster.ControlVoter
	for i := 0; i < 3; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		voters = append(voters, cluster.ControlVoter{NodeID: uint64(201 + i), Addr: listener.Addr().String()})
		require.NoError(t, listener.Close())
	}
	nodes := make([]*cluster.Node, 3)
	cfgs := make([]cluster.Config, 3)
	for i, voter := range voters {
		cfgs[i] = cluster.Config{NodeID: voter.NodeID, ListenAddr: voter.Addr, DataDir: filepath.Join(t.TempDir(), "node"), Control: cluster.ControlConfig{ClusterID: "native-v3-recovery-control", Voters: voters, AllowBootstrap: true}, Slots: cluster.SlotConfig{InitialSlotCount: 4, HashSlotCount: 256, ReplicaCount: 3}, Channel: cluster.ChannelConfig{ReplicaCount: 3}}
	}
	start := func(i int) error {
		node, err := cluster.New(cfgs[i])
		if err != nil {
			return err
		}
		nodes[i] = node
		return node.Start(ctx)
	}
	t.Cleanup(func() {
		var wg sync.WaitGroup
		for _, node := range nodes {
			if node != nil {
				wg.Add(1)
				go func(node *cluster.Node) { defer wg.Done(); _ = node.Stop(context.Background()) }(node)
			}
		}
		wg.Wait()
	})
	errs := make(chan error, 3)
	for i := range nodes {
		go func(i int) { errs <- start(i) }(i)
	}
	for range nodes {
		require.NoError(t, <-errs)
	}
	require.Eventually(t, func() bool {
		for _, node := range nodes {
			s := node.Snapshot()
			if !s.RoutesReady || !s.SlotsReady || !s.ChannelsReady {
				return false
			}
		}
		return true
	}, 15*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		probeCtx, done := context.WithTimeout(ctx, 500*time.Millisecond)
		defer done()
		return nodes[0].ProbeWriteReady(probeCtx) == nil
	}, 10*time.Second, 50*time.Millisecond)
	id := ch.ChannelID{ID: "migrationgroup", Type: 2}
	for i := uint64(1); i <= 3; i++ {
		callCtx, done := context.WithTimeout(ctx, 5*time.Second)
		result, err := nodes[0].AppendChannel(callCtx, ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: 1000 + i, ServerTimestampMS: 1788670602000, FromUID: "alice", ClientMsgNo: fmt.Sprintf("control-%d", i), Payload: []byte(fmt.Sprintf("control-%d", i))}})
		done()
		require.NoError(t, err)
		require.Equal(t, i, result.MessageSeq)
	}
	localComplete := func(node *cluster.Node) bool {
		for seq := uint64(1); seq <= 3; seq++ {
			hit, found, err := node.LookupChannelIdempotency(ctx, id, "alice", fmt.Sprintf("control-%d", seq))
			if err != nil || !found {
				return false
			}
			row := hit.Message
			if row.MessageID != 1000+seq || row.MessageSeq != seq || string(row.Payload) != fmt.Sprintf("control-%d", seq) || row.ServerTimestampMS != 1788670602000 {
				return false
			}
		}
		return true
	}

	for _, node := range nodes {

		require.Eventually(t, func() bool { return localComplete(node) }, 15*time.Second, 50*time.Millisecond, "all original replicas must be locally complete before injecting loss")
	}
	metadata, err := nodes[0].GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	failed := -1
	for i, node := range nodes {
		if node.NodeID() == metadata.Leader {
			failed = i
		}
	}
	require.NotEqual(t, -1, failed)
	t.Logf("pre-loss: all 3 local replicas complete through local idempotency primary reads, channel metadata=%+v", metadata)
	readBefore, err := nodes[0].ReadChannelCommitted(ctx, id, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Len(t, readBefore.Messages, 3)
	require.NoError(t, nodes[failed].Stop(ctx))
	nodes[failed] = nil
	messageDir := filepath.Join(cfgs[failed].DataDir, "messages")
	if empty {
		require.NoError(t, os.Rename(messageDir, messageDir+"-before-control"))
	}
	require.NoError(t, start(failed))
	var read store.ReadCommittedResult
	var readErr error
	observe := func() {
		meta, metaErr := nodes[failed].GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
		rows, _, _, localErr := nodes[failed].ReadLocalLatestMessages(ctx, 0, 10)
		t.Logf("post-loss node=%d snapshot=%+v metadata=%+v meta_error=%v routed_count=%d routed_error=%v local_count=%d local_error=%v physical_complete=%t", nodes[failed].NodeID(), nodes[failed].Snapshot(), meta, metaErr, len(read.Messages), readErr, len(rows), localErr, localComplete(nodes[failed]))
	}
	recovered := assert.Eventually(t, func() bool {
		callCtx, done := context.WithTimeout(ctx, time.Second)
		defer done()
		rows, err := nodes[failed].ReadChannelCommittedBatch(callCtx, []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20}}})
		readErr = err
		if err != nil || len(rows) != 1 {
			return false
		}
		read, readErr = rows[0].Read, rows[0].Err
		return readErr == nil && len(read.Messages) == 3 && localComplete(nodes[failed])
	}, 25*time.Second, 50*time.Millisecond, "restarted replica must expose every previously committed message")
	observe()
	if !recovered {
		// This follow-up does not change the failed acceptance assertion above.
		// It distinguishes a cold read/recovery trigger from destroyed history.
		callCtx, done := context.WithTimeout(ctx, 5*time.Second)
		appended, appendErr := nodes[failed].AppendChannel(callCtx, ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: 1004, FromUID: "alice", ClientMsgNo: "control-4", ServerTimestampMS: 1788670602000, Payload: []byte("control-4")}})
		done()
		next, nextErr := nodes[failed].ReadChannelCommitted(ctx, id, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
		t.Logf("after failed recovery assertion, isolated follow-up append: seq=%d error=%v routed_count=%d read_error=%v original_physical_complete=%t", appended.MessageSeq, appendErr, len(next.Messages), nextErr, localComplete(nodes[failed]))
	}
}
