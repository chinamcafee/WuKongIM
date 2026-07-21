//go:build e2e

package linku_v3_contract

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const tokenAuthConfigKey = "WK_GATEWAY_TOKEN_AUTH_ENABLED"

func TestSingleNodeLinkUV3APIAndWKProtoContract(t *testing.T) {
	sink := newContractWebhookSink(t)
	defer sink.Close()
	node := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, contractNodeOverrides(sink.URL())))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	requireHealthAndReadiness(t, ctx, *node)

	var route struct {
		TCPAddr string `json:"tcp_addr"`
		WSAddr  string `json:"ws_addr"`
		WSSAddr string `json:"wss_addr"`
	}
	_, err := suite.GetJSON(ctx, "http://"+node.APIAddr()+"/route?uid=linku-contract-alice", &route)
	require.NoError(t, err, node.DumpDiagnostics())
	require.NotEmpty(t, route.TCPAddr, node.DumpDiagnostics())

	registerToken(t, ctx, *node, "linku-contract-alice", "alice-token")
	registerToken(t, ctx, *node, "linku-contract-bob", "bob-token")

	alice := mustConnect(t, *node, "linku-contract-alice", "alice-token")
	defer func() { _ = alice.Close() }()
	bob := mustConnect(t, *node, "linku-contract-bob", "bob-token")
	defer func() { _ = bob.Close() }()

	const channelID = "linku-v3-contract-group"
	requireManagementStatus(t, ctx, *node, "/channel", map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "reset": 1,
		"subscribers": []string{"linku-contract-alice", "linku-contract-bob"},
	})
	requireManagementStatus(t, ctx, *node, "/channel/whitelist_set", map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"uids": []string{"linku-contract-alice"},
	})

	require.NoError(t, alice.SendFrame(&frame.SendPacket{
		ClientSeq: 1, ClientMsgNo: "linku-wkproto-v6-1", ChannelID: channelID,
		ChannelType: frame.ChannelTypeGroup, Payload: []byte("wkproto-v6-live"),
	}), node.DumpDiagnostics())
	ack, err := alice.ReadSendAck()
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	require.Equal(t, uint64(1), ack.ClientSeq)
	require.Equal(t, "linku-wkproto-v6-1", ack.ClientMsgNo)
	require.NotZero(t, ack.MessageSeq)
	recv, err := bob.ReadRecv()
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, ack.MessageSeq, recv.MessageSeq)
	require.Equal(t, "linku-wkproto-v6-1", recv.ClientMsgNo)
	require.NoError(t, bob.RecvAck(recv.MessageID, recv.MessageSeq), node.DumpDiagnostics())

	require.NoError(t, bob.SendFrame(&frame.SendPacket{
		ClientSeq: 2, ClientMsgNo: "linku-wkproto-denied-1", ChannelID: channelID,
		ChannelType: frame.ChannelTypeGroup, Payload: []byte("denied"),
	}), node.DumpDiagnostics())
	denied, err := bob.ReadSendAck()
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, frame.ReasonNotInWhitelist, denied.ReasonCode)
	require.Equal(t, "linku-wkproto-denied-1", denied.ClientMsgNo)

	postOrdinaryMessageContract(t, ctx, *node, map[string]any{
		"from_uid": "linku-contract-alice", "channel_id": channelID,
		"channel_type": frame.ChannelTypeGroup, "client_msg_no": "linku-http-ordinary-1",
		"payload": base64.StdEncoding.EncodeToString([]byte("ordinary")),
	})
	postOrdinaryMessageContract(t, ctx, *node, map[string]any{
		"from_uid": "linku-contract-alice", "channel_id": channelID,
		"channel_type": frame.ChannelTypeGroup, "no_persist": 1,
		"payload": base64.StdEncoding.EncodeToString([]byte("volatile")),
	})
	persistedSeq := postPersistedMessageContract(t, ctx, *node, map[string]any{
		"from_uid": "linku-contract-alice", "channel_id": channelID,
		"channel_type": frame.ChannelTypeGroup, "client_msg_no": "linku-http-persisted-1",
		"payload":          base64.StdEncoding.EncodeToString([]byte("persisted")),
		"wait_for_persist": 1,
	})

	var history struct {
		StartMessageSeq uint64 `json:"start_message_seq"`
		EndMessageSeq   uint64 `json:"end_message_seq"`
		More            int    `json:"more"`
		Messages        []struct {
			MessageSeq uint64 `json:"message_seq"`
		} `json:"messages"`
	}
	_, err = suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{
		"login_uid": "linku-contract-alice", "channel_id": channelID,
		"channel_type": frame.ChannelTypeGroup, "start_message_seq": 0,
		"end_message_seq": 0, "limit": 20, "pull_mode": 0,
	}, &history)
	require.NoError(t, err, node.DumpDiagnostics())
	require.NotEmpty(t, history.Messages, node.DumpDiagnostics())
	require.Contains(t, messageSequences(history.Messages), persistedSeq)

	var conversations []struct {
		ChannelID  string `json:"channel_id"`
		LastMsgSeq uint64 `json:"last_msg_seq"`
		Recents    []struct {
			MessageSeq uint64 `json:"message_seq"`
		} `json:"recents"`
	}
	_, err = suite.PostJSON(ctx, "http://"+node.APIAddr()+"/conversation/sync", map[string]any{
		"uid": "linku-contract-alice", "msg_count": 5, "limit": 20,
	}, &conversations)
	require.NoError(t, err, node.DumpDiagnostics())
	require.True(t, containsConversation(conversations, channelID, persistedSeq), node.DumpDiagnostics())

	event := sink.RequireEvent(t, "msg.notify", 10*time.Second)
	require.NotEmpty(t, event.ID)
	require.Equal(t, "1", event.Attempt)
	require.True(t, json.Valid(event.Body), "webhook body = %s", event.Body)
}

func TestThreeNodeLinkUV3ReadinessTokenAuthAndOutboxGate(t *testing.T) {
	sink := newContractWebhookSink(t)
	defer sink.Close()
	overrides := contractNodeOverrides(sink.URL())
	cluster := suite.New(t).StartThreeNodeCluster(
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	for _, node := range cluster.Nodes {
		requireHealthAndReadiness(t, ctx, node)
		value, err := suite.FetchMetricValue(ctx, node.APIAddr(), "wukongim_webhook_outbox_backlog", nil)
		require.NoError(t, err, node.DumpDiagnostics())
		require.GreaterOrEqual(t, value, float64(0))
	}

	registerToken(t, ctx, *cluster.MustNode(1), "linku-contract-routed", "routed-token")
	client := mustConnect(t, *cluster.MustNode(2), "linku-contract-routed", "routed-token")
	defer func() { _ = client.Close() }()

	postPersistedMessageContract(t, ctx, *cluster.MustNode(3), map[string]any{
		"from_uid": "linku-contract-routed", "channel_id": "linku-contract-offline",
		"channel_type": frame.ChannelTypePerson, "client_msg_no": "linku-three-node-outbox-1",
		"payload":          base64.StdEncoding.EncodeToString([]byte("three-node-outbox")),
		"wait_for_persist": 1,
	})
	event := sink.RequireEvent(t, "msg.notify", 15*time.Second)
	require.NotEmpty(t, event.ID)
	require.Equal(t, "1", event.Attempt)
}

func contractNodeOverrides(webhookURL string) map[string]string {
	return map[string]string{
		tokenAuthConfigKey:                      "true",
		"WK_WEBHOOK_HTTP_ADDR":                  webhookURL,
		"WK_WEBHOOK_FOCUS_EVENTS":               `["msg.notify","msg.offline"]`,
		"WK_WEBHOOK_QUEUE_SIZE":                 "64",
		"WK_WEBHOOK_WORKERS":                    "1",
		"WK_WEBHOOK_REQUEST_TIMEOUT":            "2s",
		"WK_WEBHOOK_RETRY_MAX_ATTEMPTS":         "2",
		"WK_WEBHOOK_OUTBOX_MAX_ENTRIES":         "1000",
		"WK_WEBHOOK_OUTBOX_MAX_BYTES":           "10485760",
		"WK_WEBHOOK_OUTBOX_DISPATCH_BATCH_SIZE": "32",
	}
}

func requireHealthAndReadiness(t *testing.T, ctx context.Context, node suite.StartedNode) {
	t.Helper()
	var health struct {
		Status string `json:"status"`
	}
	_, err := suite.GetJSON(ctx, "http://"+node.APIAddr()+"/healthz", &health)
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, "ok", health.Status)
	var ready struct {
		Ready bool `json:"ready"`
	}
	_, err = suite.GetJSON(ctx, "http://"+node.APIAddr()+"/readyz", &ready)
	require.NoError(t, err, node.DumpDiagnostics())
	require.True(t, ready.Ready, node.DumpDiagnostics())
}

func registerToken(t *testing.T, ctx context.Context, node suite.StartedNode, uid, token string) {
	t.Helper()
	requireManagementStatus(t, ctx, node, "/user/token", map[string]any{
		"uid": uid, "token": token, "device_flag": frame.APP, "device_level": frame.DeviceLevelMaster,
	})
}

func mustConnect(t *testing.T, node suite.StartedNode, uid, token string) *suite.WKProtoClient {
	t.Helper()
	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	connack, err := client.ConnectWithTokenContext(context.Background(), node.GatewayAddr(), uid, uid+"-device", token, frame.APP)
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, uint8(frame.LatestVersion), connack.ServerVersion)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)
	return client
}

func requireManagementStatus(t *testing.T, ctx context.Context, node suite.StartedNode, path string, request any) {
	t.Helper()
	var response map[string]json.RawMessage
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+path, request, &response)
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, []string{"status"}, sortedKeys(response))
	require.JSONEq(t, `200`, string(response["status"]))
}

func postOrdinaryMessageContract(t *testing.T, ctx context.Context, node suite.StartedNode, request any) uint64 {
	t.Helper()
	var response map[string]json.RawMessage
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/message/send", request, &response)
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, []string{"message_id", "message_seq", "reason"}, sortedKeys(response))
	var reason uint8
	require.NoError(t, json.Unmarshal(response["reason"], &reason))
	require.Equal(t, uint8(frame.ReasonSuccess), reason)
	var seq uint64
	require.NoError(t, json.Unmarshal(response["message_seq"], &seq))
	return seq
}

func postPersistedMessageContract(t *testing.T, ctx context.Context, node suite.StartedNode, request any) uint64 {
	t.Helper()
	var response map[string]json.RawMessage
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/message/send", request, &response)
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, []string{"client_msg_no", "deduplicated", "message_id", "message_seq", "status"}, sortedKeys(response))
	var status int
	var seq uint64
	var messageID string
	require.NoError(t, json.Unmarshal(response["status"], &status))
	require.NoError(t, json.Unmarshal(response["message_seq"], &seq))
	require.NoError(t, json.Unmarshal(response["message_id"], &messageID))
	require.Equal(t, http.StatusOK, status)
	require.NotZero(t, seq)
	require.NotEmpty(t, messageID)
	return seq
}

func sortedKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func messageSequences(messages []struct {
	MessageSeq uint64 `json:"message_seq"`
}) []uint64 {
	sequences := make([]uint64, 0, len(messages))
	for _, message := range messages {
		sequences = append(sequences, message.MessageSeq)
	}
	return sequences
}

func containsConversation(conversations []struct {
	ChannelID  string `json:"channel_id"`
	LastMsgSeq uint64 `json:"last_msg_seq"`
	Recents    []struct {
		MessageSeq uint64 `json:"message_seq"`
	} `json:"recents"`
}, channelID string, minimumSeq uint64) bool {
	for _, conversation := range conversations {
		if conversation.ChannelID == channelID && conversation.LastMsgSeq >= minimumSeq {
			return true
		}
	}
	return false
}

type contractWebhookEvent struct {
	Event   string
	ID      string
	Attempt string
	Body    []byte
}

type contractWebhookSink struct {
	server *httptest.Server
	mu     sync.Mutex
	events []contractWebhookEvent
}

func newContractWebhookSink(t *testing.T) *contractWebhookSink {
	t.Helper()
	sink := &contractWebhookSink{}
	sink.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sink.mu.Lock()
		sink.events = append(sink.events, contractWebhookEvent{
			Event: r.URL.Query().Get("event"), ID: r.Header.Get("X-WK-Webhook-ID"),
			Attempt: r.Header.Get("X-WK-Webhook-Attempt"), Body: append([]byte(nil), body...),
		})
		sink.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return sink
}

func (s *contractWebhookSink) URL() string { return s.server.URL + "/webhook" }

func (s *contractWebhookSink) Close() { s.server.Close() }

func (s *contractWebhookSink) RequireEvent(t *testing.T, event string, timeout time.Duration) contractWebhookEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, candidate := range s.events {
			if candidate.Event == event && strings.TrimSpace(candidate.ID) != "" {
				s.mu.Unlock()
				return candidate
			}
		}
		snapshot := fmt.Sprintf("%#v", s.events)
		s.mu.Unlock()
		if time.Now().Add(50 * time.Millisecond).After(deadline) {
			t.Fatalf("webhook %s not observed: %s", event, snapshot)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("webhook %s timed out", event)
	return contractWebhookEvent{}
}
