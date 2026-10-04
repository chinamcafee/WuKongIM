//go:build e2e && (darwin || linux)

package channel_failover

import (
	"context"
	"encoding/base64"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// TestChannelThreeNodeLeaderFailoverWhileProcessPaused checks availability before
// the paused process resumes. History safety alone does not satisfy this gate.
func TestChannelThreeNodeLeaderFailoverWhileProcessPaused(t *testing.T) {
	runPausedLeaderScenario(t, true)
}

// TestChannelAcknowledgedHistorySurvivesProcessPause permits bounded write
// unavailability but never loss or sequence reuse after incomplete recovery.
func TestChannelAcknowledgedHistorySurvivesProcessPause(t *testing.T) {
	runPausedLeaderScenario(t, false)
}

func runPausedLeaderScenario(t *testing.T, requireAvailable bool) {
	t.Helper()
	cluster := suite.New(t).StartThreeNodeCluster(fastRecoveryOptionsForNodes(3, map[string]string{
		"WK_CLUSTER_INITIAL_SLOT_COUNT": "10", "WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK": "1",
	})...)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	survivor := cluster.MustNode(1)
	slots := cluster.ManagerClient(t, 1).MustSlots(t)
	var affected failoverChannel
	var bootstrap sentMessage
	for _, slot := range slots {
		if slot.Assignment.PreferredLeaderID != 3 {
			continue
		}
		id, ok := channelIDForPhysicalSlot(slots, slot, fmt.Sprintf("e2e-pause-%d", time.Now().UnixNano()))
		require.True(t, ok)
		createGroupChannel(t, survivor, id, frame.ChannelTypeGroup)
		bootstrap = sendGroupMessage(t, survivor, id, frame.ChannelTypeGroup, "bootstrap")
		meta := suite.RequireChannelRuntimeMetaEventually(t, cluster, survivor, id, frame.ChannelTypeGroup, 20*time.Second)
		require.Equal(t, uint64(3), meta.Leader)
		require.ElementsMatch(t, []uint64{1, 2, 3}, meta.Replicas)
		require.ElementsMatch(t, []uint64{1, 2, 3}, meta.ISR)
		require.Equal(t, int64(2), meta.MinISR)
		affected = failoverChannel{ChannelID: id, ChannelType: frame.ChannelTypeGroup, Leader: 3}
		break
	}
	require.NotEmpty(t, affected.ChannelID)
	process := cluster.MustNode(3).Process.Cmd.Process
	require.NotNil(t, process)
	t.Cleanup(func() { _ = process.Signal(syscall.SIGCONT) })
	// Pause immediately after ACK, before a history read can give the trailing
	// replica extra time to catch up. The observed metadata already proves RF3.
	affected.Pre = sendGroupMessage(t, survivor, affected.ChannelID, affected.ChannelType, "before-pause")
	require.NoError(t, process.Signal(syscall.SIGSTOP))
	time.Sleep(fastRecoveryHealthReportTTL + fastRecoveryHealthReportInterval)
	timeout := 20 * time.Second
	if requireAvailable {
		timeout = 60 * time.Second
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), timeout)
	key := fmt.Sprintf("while-paused-%d", time.Now().UnixNano())
	body := map[string]any{
		"from_uid": "channel-ha-sender", "channel_id": affected.ChannelID, "channel_type": affected.ChannelType,
		"client_msg_no": key, "payload": base64.StdEncoding.EncodeToString([]byte("while-paused")),
	}
	response, sendErr := suite.PostMessageSendEventually(sendCtx, survivor.APIAddr(), body)
	sendCancel()
	if requireAvailable {
		require.NoError(t, sendErr, cluster.DumpDiagnostics())
	}
	var duringPause []sentMessage
	if sendErr == nil {
		require.Equal(t, uint8(frame.ReasonSuccess), response.Reason)
		require.Greater(t, response.MessageSeq, affected.Pre.Seq)
		retryCtx, retryCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer retryCancel()
		retried, retryErr := suite.PostMessageSendEventually(retryCtx, cluster.MustNode(2).APIAddr(), body)
		require.NoError(t, retryErr, cluster.DumpDiagnostics())
		require.Equal(t, response.MessageID, retried.MessageID)
		require.Equal(t, response.MessageSeq, retried.MessageSeq)
		last := response.MessageSeq
		for i := 0; i < 3; i++ {
			message := sendGroupMessageWithin(t, cluster.MustNode(uint64(1+i%2)), affected.ChannelID, affected.ChannelType, fmt.Sprintf("continued-while-paused-%d", i), 15*time.Second)
			require.Greater(t, message.Seq, last)
			last = message.Seq
			duringPause = append(duringPause, message)
		}
		t.Log("four distinct sends and an exact cross-ingress retry committed while node 3 remained paused")
	} else {
		t.Logf("write remained unavailable during pause: %v", sendErr)
	}
	require.NoError(t, process.Signal(syscall.SIGCONT))
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer readyCancel()
	require.NoError(t, cluster.WaitHTTPReady(readyCtx), cluster.DumpDiagnostics())
	// Exercise routing through the resumed former leader itself.
	recovered := sendGroupMessageWithin(t, cluster.MustNode(3), affected.ChannelID, affected.ChannelType, "after-pause", 30*time.Second)
	require.Greater(t, recovered.Seq, affected.Pre.Seq)
	if sendErr == nil {
		require.Greater(t, recovered.Seq, response.MessageSeq)
		requireMessageOnceEventually(t, cluster, survivor, affected.ChannelID, affected.ChannelType, sentMessage{ID: uint64(response.MessageID), Seq: response.MessageSeq, ClientMsgNo: key})
	}
	for _, message := range duringPause {
		require.Greater(t, recovered.Seq, message.Seq)
		requireMessageOnceEventually(t, cluster, survivor, affected.ChannelID, affected.ChannelType, message)
	}
	requireMessageOnceEventually(t, cluster, survivor, affected.ChannelID, affected.ChannelType, bootstrap)
	requireMessageOnceEventually(t, cluster, survivor, affected.ChannelID, affected.ChannelType, affected.Pre)
	requireMessageOnceEventually(t, cluster, survivor, affected.ChannelID, affected.ChannelType, recovered)
	if requireAvailable {
		expected := append([]sentMessage{bootstrap, affected.Pre, recovered, {ID: uint64(response.MessageID), Seq: response.MessageSeq, ClientMsgNo: key}}, duringPause...)
		requirePausedHistoryPagination(t, cluster.MustNode(3), affected, expected)
	}
}

// Read through the same More flag and oldest visible sequence used by clients;
// exact sequence windows would conceal an incorrect end-of-history response.
func requirePausedHistoryPagination(t *testing.T, node *suite.StartedNode, channel failoverChannel, want []sentMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	expected := make(map[string]sentMessage, len(want))
	for _, m := range want {
		expected[m.ClientMsgNo] = m
	}
	seen := make(map[string]bool, len(want))
	cursor := uint64(0)
	finished := false
	for pageNo := 0; pageNo < 10; pageNo++ {
		var page struct {
			More     int `json:"more"`
			Messages []struct {
				ID     string `json:"message_idstr"`
				Seq    uint64 `json:"message_seq"`
				Client string `json:"client_msg_no"`
			} `json:"messages"`
		}
		_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": "channel-ha-member-a", "channel_id": channel.ChannelID, "channel_type": channel.ChannelType, "start_message_seq": cursor, "end_message_seq": 0, "pull_mode": 0, "limit": 2}, &page)
		require.NoError(t, err, node.DumpDiagnostics())
		require.NotEmpty(t, page.Messages)
		for _, m := range page.Messages {
			old, ok := expected[m.Client]
			require.True(t, ok, "internal control records must remain hidden")
			require.False(t, seen[m.Client])
			require.Equal(t, fmt.Sprint(old.ID), m.ID)
			require.Equal(t, old.Seq, m.Seq)
			seen[m.Client] = true
		}
		if page.More == 0 {
			finished = true
			break
		}
		require.Len(t, page.Messages, 2)
		require.Greater(t, page.Messages[0].Seq, uint64(1))
		cursor = page.Messages[0].Seq - 1
	}
	require.True(t, finished)
	require.Len(t, seen, len(expected), "More must not hide acknowledged messages behind recovery barriers")
}
