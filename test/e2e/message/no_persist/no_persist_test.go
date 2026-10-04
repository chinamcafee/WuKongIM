//go:build e2e

package no_persist

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrdinaryNoPersistDelivery checks HTTP sends against real WKProto online
// sessions, including remote Channel authority and remote recipient ownership.
func TestOrdinaryNoPersistDelivery(t *testing.T) {
	for _, topology := range []struct {
		name  string
		count int
	}{{"single-node cluster", 1}, {"three-node cluster", 3}} {
		t.Run(topology.name, func(t *testing.T) {
			count := topology.count
			s := suite.New(t)
			var nodes []*suite.StartedNode
			var options []suite.Option
			for i := 1; i <= count; i++ {
				options = append(options, suite.WithNodeConfigOverrides(uint64(i), map[string]string{
					"WK_GATEWAY_TOKEN_AUTH_ON":   "false",
					"WK_CLUSTER_HASH_SLOT_COUNT": "256",
				}))
			}
			if count == 1 {
				nodes = []*suite.StartedNode{s.StartSingleNodeCluster(options...)}
			} else {
				c := s.StartThreeNodeCluster(append(options, suite.WithManagerHTTP())...)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				require.NoError(t, c.WaitClusterReady(ctx), c.DumpDiagnostics())
				_, err := c.WaitSlotLeadersStable(ctx, time.Second)
				require.NoError(t, err, c.DumpDiagnostics())
				nodes = []*suite.StartedNode{c.MustNode(1), c.MustNode(2), c.MustNode(3)}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			connect := func(node *suite.StartedNode, uid string) *suite.WKProtoClient {
				t.Helper()
				client, err := suite.NewWKProtoClient()
				require.NoError(t, err)
				require.NoError(t, client.Connect(node.GatewayAddr(), uid, uid+"-device"))
				t.Cleanup(func() { _ = client.Close() })
				return client
			}
			const senderUID, receiverUID = "nopersist-sender", "nopersist-receiver"
			connect(nodes[0], senderUID)
			receiver := connect(nodes[len(nodes)-1], receiverUID)
			for _, kind := range []uint8{frame.ChannelTypeGroup, frame.ChannelTypePerson} {
				t.Run(fmt.Sprintf("channel-type-%d", kind), func(t *testing.T) {
					channelID, receiveChannelID := "nopersist-room", "nopersist-room"
					if kind == frame.ChannelTypePerson {
						channelID, receiveChannelID = receiverUID, senderUID
					} else {
						require.NoError(t, suite.PostChannel(ctx, nodes[0].APIAddr(), map[string]any{
							"channel_id": channelID, "channel_type": kind, "reset": 1,
							"subscribers": []string{senderUID, receiverUID},
						}))
					}
					sendHTTP := func(node *suite.StartedNode, label string, transient bool) suite.MessageSendResponse {
						t.Helper()
						noPersist := 0
						if transient {
							noPersist = 1
						}
						result, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
							"from_uid": senderUID, "channel_id": channelID, "channel_type": kind,
							"client_msg_no": fmt.Sprintf("%d-%s", kind, label),
							"payload":       base64.StdEncoding.EncodeToString([]byte(label)),
							"header":        map[string]int{"no_persist": noPersist, "sync_once": 0, "red_dot": 1},
						})
						require.NoError(t, err)
						require.Equal(t, uint8(frame.ReasonSuccess), result.Reason)
						return result
					}
					receive := func(label string, id int64, seq uint64, transient bool) {
						t.Helper()
						recv, err := receiver.ReadRecv()
						require.NoError(t, err, "online subscriber must receive %s (HTTP id=%d seq=%d)", label, id, seq)
						require.Equal(t, []byte(label), recv.Payload)
						require.Equal(t, id, recv.MessageID)
						require.Equal(t, seq, recv.MessageSeq)
						require.Equal(t, receiveChannelID, recv.ChannelID)
						require.Equal(t, senderUID, recv.FromUID)
						require.Equal(t, kind, recv.ChannelType)
						require.Equal(t, transient, recv.NoPersist)
						require.False(t, recv.SyncOnce)
						require.NoError(t, receiver.RecvAck(recv.MessageID, recv.MessageSeq))
					}
					// A durable control first proves membership, online presence, and delivery.
					first := sendHTTP(nodes[0], "before", false)
					require.EqualValues(t, 1, first.MessageSeq)
					receive("before", first.MessageID, first.MessageSeq, false)
					for i, node := range nodes {
						label := fmt.Sprintf("transient-http-%d", i)
						result := sendHTTP(node, label, true)
						t.Logf("%s: reason=%d message_id=%d message_seq=%d", label, result.Reason, result.MessageID, result.MessageSeq)
						require.Zero(t, result.MessageSeq)
						receive(label, result.MessageID, result.MessageSeq, true)
						require.NotZero(t, result.MessageID)
					}
					last := sendHTTP(nodes[0], "after", false)
					require.EqualValues(t, 2, last.MessageSeq, "transient sends must not advance the Channel log")
					receive("after", last.MessageID, last.MessageSeq, false)
					var history struct {
						Messages []struct {
							Payload []byte `json:"payload"`
						} `json:"messages"`
					}
					// Person directory admission completes asynchronously; wait for the
					// public membership-backed history view before checking its contents.
					require.EventuallyWithT(t, func(collect *assert.CollectT) {
						_, err := suite.PostJSON(ctx, "http://"+nodes[0].APIAddr()+"/channel/messagesync", map[string]any{
							"login_uid": receiverUID, "channel_id": receiveChannelID, "channel_type": kind, "limit": 20,
						}, &history)
						require.NoError(collect, err)
						require.Len(collect, history.Messages, 2, "transient messages must not appear in committed history")
					}, 5*time.Second, 50*time.Millisecond)
					require.ElementsMatch(t, [][]byte{[]byte("before"), []byte("after")}, [][]byte{history.Messages[0].Payload, history.Messages[1].Payload})
					if kind == frame.ChannelTypeGroup {
						rejected, err := suite.PostMessageSend(ctx, nodes[len(nodes)-1].APIAddr(), map[string]any{
							"from_uid": "nopersist-outsider", "channel_id": channelID, "channel_type": kind,
							"header": map[string]int{"no_persist": 1}, "payload": "ZGVuaWVk",
						})
						require.NoError(t, err)
						require.NotEqual(t, uint8(frame.ReasonSuccess), rejected.Reason)
						require.Zero(t, rejected.MessageID)
					}
				})
			}
		})
	}
}
