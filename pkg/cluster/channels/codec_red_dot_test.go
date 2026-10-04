package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/stretchr/testify/require"
)

func TestRedDotCodecPreservesFlagsAndRejectsLossyLegacyFrames(t *testing.T) {
	for _, redDot := range []bool{false, true} {
		msg := ch.Message{MessageID: 11, MessageSeq: 1, ChannelID: "room", ChannelType: 2, RedDot: redDot, Payload: []byte("body")}
		req := ch.AppendBatchRequest{ChannelID: ch.ChannelID{ID: "room", Type: 2}, Messages: []ch.Message{msg}, ServerAllocatedMessageIDs: true}
		data, err := encodeAppendBatchRequest(req)
		require.NoError(t, err)
		got, err := decodeAppendBatchRequest(data)
		require.NoError(t, err)
		require.Equal(t, req, got)
		payloads := []struct {
			kind   uint8
			value  any
			target any
		}{
			{kindAppendResponse, ch.AppendResult{Message: msg}, &ch.AppendResult{}},
			{kindAppendBatchResponse, ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{{Message: msg}}}, &ch.AppendBatchResult{}},
			{kindPullResponse, channeltransport.PullResponse{Records: []ch.Record{{ID: 11, Index: 1, RedDot: redDot}}}, &channeltransport.PullResponse{}},
			{kindPullBatchResponse, channeltransport.PullBatchResponse{Items: []channeltransport.PullBatchItemResult{{Response: channeltransport.PullResponse{Records: []ch.Record{{ID: 11, Index: 1, RedDot: redDot}}}}}}, &channeltransport.PullBatchResponse{}},
			{kindLastVisibleResponse, LastVisibleResponse{Message: msg, Found: true}, &LastVisibleResponse{}},
			{kindConversationHeadsResponse, ConversationHeadsResponse{Items: []ConversationHeadResult{{Head: ConversationHead{Message: msg, Found: true}}}}, &ConversationHeadsResponse{}},
		}
		for _, p := range payloads {
			data, err := encodeRPCResult(p.kind, p.value, nil)
			require.NoError(t, err)
			require.NoError(t, decodeRPCResult(data, p.kind, p.target))
			require.Equal(t, p.value, dereferenceRedDotPayload(p.target))
			for _, version := range []uint8{5, 6, 7} {
				legacy, err := encodeRPCResultVersion(version, p.kind, p.value, nil)
				require.NoError(t, err)
				err = decodeRPCResult(legacy, p.kind, p.target)
				if redDot {
					require.ErrorContains(t, err, "red_dot requires")
				} else {
					require.NoError(t, err)
				}
			}
		}
		for _, version := range []uint8{5, 6, 7} {
			data, err := encodeAppendBatchRequestVersion(req, version)
			if redDot {
				require.ErrorIs(t, err, errRedDotCodecRequired)
				require.Nil(t, data)
				continue
			}
			require.NoError(t, err)
			got, err := decodeAppendBatchRequest(data)
			require.NoError(t, err)
			require.False(t, got.Messages[0].RedDot)
			require.Equal(t, version >= 7, got.ServerAllocatedMessageIDs)
			require.Equal(t, version, responseCodecVersion(data))
		}
	}
}

func dereferenceRedDotPayload(v any) any {
	switch p := v.(type) {
	case *ch.AppendResult:
		return *p
	case *ch.AppendBatchResult:
		return *p
	case *channeltransport.PullResponse:
		return *p
	case *channeltransport.PullBatchResponse:
		return *p
	case *LastVisibleResponse:
		return *p
	case *ConversationHeadsResponse:
		return *p
	}
	panic("unexpected test payload")
}

func TestRedDotAppendFallbackNeverCallsLegacyPeerWithLossyPayload(t *testing.T) {
	client := &TransportClient{}
	calls := 0
	req := ch.AppendRequest{Message: ch.Message{RedDot: true}}
	_, err := client.callCompatible(2, false, func(version uint8) ([]byte, error) {
		return encodeAppendRequestVersion(req, version)
	}, func(payload []byte) ([]byte, error) {
		calls++
		require.Equal(t, linkuCodecVersion, payload[0])
		return nil, errInvalidCodecFrame
	})
	require.ErrorIs(t, err, errRedDotCodecRequired)
	require.Equal(t, 1, calls)
	_, err = client.ForwardAppend(context.Background(), 2, req)
	require.ErrorIs(t, err, errRedDotCodecRequired, "cached legacy selection must fail before transport")
}
