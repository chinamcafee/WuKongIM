package channels

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/stretchr/testify/require"
)

// Recovery barriers and CMD records already carry SyncOnce in native storage.
// A node RPC must preserve the flag so the receiving history reader can apply
// the existing ordinary-message visibility policy.
func TestSyncOnceSurvivesNativeChannelRPC(t *testing.T) {
	for _, syncOnce := range []bool{false, true} {
		msg := ch.Message{MessageID: 17, MessageSeq: 4, ChannelID: "native-history", ChannelType: 2, SyncOnce: syncOnce, Payload: []byte("native record")}
		req := ch.AppendBatchRequest{Messages: []ch.Message{msg}}
		wire, err := encodeAppendBatchRequest(req)
		require.NoError(t, err)
		got, err := decodeAppendBatchRequest(wire)
		require.NoError(t, err)
		require.Equal(t, req, got)

		reads := CommittedReadsResponse{Items: []CommittedReadResult{{Read: channelstore.ReadCommittedResult{Messages: []ch.Message{msg}}}}}
		wire, err = encodeRPCResult(kindCommittedReadsResponse, reads, nil)
		require.NoError(t, err)
		var received CommittedReadsResponse
		require.NoError(t, decodeRPCResult(wire, kindCommittedReadsResponse, &received))
		require.Equal(t, reads, received)

		pull := channeltransport.PullResponse{Records: []ch.Record{{ID: 17, Index: 4, SyncOnce: syncOnce, Payload: msg.Payload}}}
		wire, err = encodeRPCResult(kindPullResponse, pull, nil)
		require.NoError(t, err)
		var pulled channeltransport.PullResponse
		require.NoError(t, decodeRPCResult(wire, kindPullResponse, &pulled))
		require.Equal(t, pull, pulled)
	}
}

func TestSyncOnceRejectsLossyLegacyChannelRPC(t *testing.T) {
	msg := ch.Message{MessageID: 17, SyncOnce: true}
	for _, version := range []uint8{5, 6, 7} {
		wire, err := encodeAppendRequestVersion(ch.AppendRequest{Message: msg}, version)
		require.ErrorIs(t, err, errSyncOnceCodecRequired)
		require.Nil(t, wire)
		wire, err = encodeAppendBatchRequestVersion(ch.AppendBatchRequest{Messages: []ch.Message{msg}}, version)
		require.ErrorIs(t, err, errSyncOnceCodecRequired)
		require.Nil(t, wire)
		reads := CommittedReadsResponse{Items: []CommittedReadResult{{Read: channelstore.ReadCommittedResult{Messages: []ch.Message{msg}}}}}
		wire, err = encodeRPCResultVersion(version, kindCommittedReadsResponse, reads, nil)
		require.NoError(t, err)
		var received CommittedReadsResponse
		require.ErrorContains(t, decodeRPCResult(wire, kindCommittedReadsResponse, &received), "sync_once requires")
	}
}
