package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/require"
)

func TestMessageSendWaitForPersistIsIdempotent(t *testing.T) {
	var notifyWebhookCount atomic.Int32
	var offlineWebhookCount atomic.Int32
	webhookServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if !bytes.Contains(body, []byte("relation-system")) {
			writer.WriteHeader(http.StatusOK)
			return
		}
		switch request.URL.Query().Get("event") {
		case "msg.notify":
			notifyWebhookCount.Add(1)
		case "msg.offline":
			offlineWebhookCount.Add(1)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(webhookServer.Close)

	httpAddr := reserveTestAddr(t)
	clusterAddr := reserveTestAddr(t)
	tcpAddr := reserveTestAddr(t)
	wsAddr := reserveTestAddr(t)
	server := NewTestServer(t,
		options.WithDemoOn(false),
		options.WithManagerOn(false),
		options.WithHTTPAddr(httpAddr),
		options.WithClusterAPIURL("http://"+httpAddr),
		options.WithAddr("tcp://"+tcpAddr),
		options.WithWSAddr("ws://"+wsAddr),
		options.WithClusterAddr("tcp://"+clusterAddr),
		options.WithClusterServerAddr(clusterAddr),
		options.WithWebhookHTTPAddr(webhookServer.URL),
		options.WithWebhookMsgNotifyEventPushInterval(20*time.Millisecond),
		options.WithWebhookMsgNotifyEventCountPerPush(1),
	)
	server.opts.Webhook.FocusEvents = []string{"msg.notify", "msg.offline"}
	server.opts.Mode = options.TestMode
	require.NoError(t, server.Start())
	t.Cleanup(server.StopNoErr)
	server.MustWaitAllSlotsReady(10 * time.Second)
	// MustWaitAllSlotsReady 只保证配置中的 leader 已就绪，给本地 slot raft 一次 tick 完成唤醒。
	time.Sleep(500 * time.Millisecond)
	provisionTestDevice(t, "user-a")
	provisionTestDevice(t, "user-b")
	provisionPersonAllowlist(t, "user-b", "user-a")

	clientA := client.New("tcp://"+tcpAddr, client.WithUID("user-a"))
	clientB := client.New("tcp://"+tcpAddr, client.WithUID("user-b"))
	require.NoError(t, clientA.Connect())
	require.NoError(t, clientB.Connect())
	t.Cleanup(clientA.Close)
	t.Cleanup(clientB.Close)
	var receivedByA atomic.Int32
	var receivedByB atomic.Int32
	received := make(chan string, 4)
	ordinaryReceived := make(chan struct{}, 1)
	clientA.SetOnRecv(func(packet *wkproto.RecvPacket) error {
		if packet.ClientMsgNo == "relation-system-once-1" {
			receivedByA.Add(1)
			received <- "user-a"
		}
		return nil
	})
	clientB.SetOnRecv(func(packet *wkproto.RecvPacket) error {
		if packet.ClientMsgNo == "relation-system-once-1" {
			receivedByB.Add(1)
			received <- "user-b"
		}
		if string(packet.Payload) == "ordinary-chat-regression" {
			ordinaryReceived <- struct{}{}
		}
		return nil
	})
	require.NoError(t, clientA.SendMessage(
		client.NewChannel("user-b", wkproto.ChannelTypePerson),
		[]byte("ordinary-chat-regression"),
		client.SendOptionWithClientMsgNo("ordinary-chat-regression-1"),
	))
	select {
	case <-ordinaryReceived:
	case <-time.After(3 * time.Second):
		t.Fatal("普通个人聊天消息回归超时")
	}

	request := map[string]any{
		"header": map[string]any{
			"no_persist": 0,
			"red_dot":    1,
		},
		"client_msg_no":      "relation-system-once-1",
		"from_uid":           "user-a",
		"channel_id":         "user-b",
		"channel_type":       wkproto.ChannelTypePerson,
		"payload":            []byte(`{"type":1001,"content":"friend relation changed"}`),
		"wait_for_persist":   1,
		"persist_timeout_ms": 3000,
	}

	status, first := postPersistMessage(t, httpAddr, request)
	require.Equal(t, http.StatusOK, status, first)
	require.Equal(t, float64(0), first["deduplicated"])
	require.NotEmpty(t, first["message_id"])
	require.Greater(t, first["message_seq"].(float64), float64(0))
	require.ElementsMatch(t, []string{"user-a", "user-b"}, []string{
		waitForReceivedUser(t, received),
		waitForReceivedUser(t, received),
	})
	waitForCounterAtLeast(t, &notifyWebhookCount, 1, "首次消息 msg.notify")

	status, duplicate := postPersistMessage(t, httpAddr, request)
	require.Equal(t, http.StatusOK, status, duplicate)
	require.Equal(t, float64(1), duplicate["deduplicated"])
	require.Equal(t, first["message_id"], duplicate["message_id"])
	require.Equal(t, first["message_seq"], duplicate["message_seq"])
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int32(1), receivedByA.Load())
	require.Equal(t, int32(1), receivedByB.Load())
	require.Equal(t, int32(1), notifyWebhookCount.Load())
	require.Equal(t, int32(0), offlineWebhookCount.Load())

	fakeChannelID := options.GetFakeChannelIDWith("user-a", "user-b")
	messages, err := service.Store.LoadLastMsgs(fakeChannelID, wkproto.ChannelTypePerson, 10)
	require.NoError(t, err)
	require.Len(t, messages, 2)

	concurrentRequest := cloneMessageRequest(t, request)
	concurrentRequest["client_msg_no"] = "relation-system-concurrent-1"
	const concurrentRequests = 16
	statuses := make(chan int, concurrentRequests)
	bodies := make(chan map[string]any, concurrentRequests)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for i := 0; i < concurrentRequests; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			concurrentStatus, concurrentBody := postPersistMessage(t, httpAddr, concurrentRequest)
			statuses <- concurrentStatus
			bodies <- concurrentBody
		}()
	}
	close(start)
	waitGroup.Wait()
	close(statuses)
	close(bodies)
	for concurrentStatus := range statuses {
		require.Equal(t, http.StatusOK, concurrentStatus)
	}
	firstPersistCount := 0
	for concurrentBody := range bodies {
		if concurrentBody["deduplicated"] == float64(0) {
			firstPersistCount++
		}
	}
	require.Equal(t, 1, firstPersistCount)
	messages, err = service.Store.LoadLastMsgs(fakeChannelID, wkproto.ChannelTypePerson, 10)
	require.NoError(t, err)
	require.Len(t, messages, 3)
	waitForCounterAtLeast(t, &notifyWebhookCount, 2, "并发同键消息 msg.notify")

	clientB.Close()
	time.Sleep(300 * time.Millisecond)
	offlineBRequest := cloneMessageRequest(t, request)
	offlineBRequest["client_msg_no"] = "relation-system-offline-b-1"
	status, offlineBFirst := postPersistMessage(t, httpAddr, offlineBRequest)
	require.Equal(t, http.StatusOK, status, offlineBFirst)
	require.Equal(t, float64(0), offlineBFirst["deduplicated"])
	waitForCounterAtLeast(t, &notifyWebhookCount, 3, "一方离线首次消息 msg.notify")
	waitForCounterAtLeast(t, &offlineWebhookCount, 1, "一方离线首次消息 msg.offline")
	status, offlineBDuplicate := postPersistMessage(t, httpAddr, offlineBRequest)
	require.Equal(t, http.StatusOK, status, offlineBDuplicate)
	require.Equal(t, float64(1), offlineBDuplicate["deduplicated"])
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int32(3), notifyWebhookCount.Load())
	require.Equal(t, int32(1), offlineWebhookCount.Load())

	clientA.Close()
	time.Sleep(300 * time.Millisecond)
	offlineBothRequest := cloneMessageRequest(t, request)
	offlineBothRequest["client_msg_no"] = "relation-system-offline-both-1"
	status, offlineBothFirst := postPersistMessage(t, httpAddr, offlineBothRequest)
	require.Equal(t, http.StatusOK, status, offlineBothFirst)
	require.Equal(t, float64(0), offlineBothFirst["deduplicated"])
	waitForCounterAtLeast(t, &notifyWebhookCount, 4, "双方离线首次消息 msg.notify")
	waitForCounterAtLeast(t, &offlineWebhookCount, 2, "双方离线首次消息 msg.offline")
	status, offlineBothDuplicate := postPersistMessage(t, httpAddr, offlineBothRequest)
	require.Equal(t, http.StatusOK, status, offlineBothDuplicate)
	require.Equal(t, float64(1), offlineBothDuplicate["deduplicated"])
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int32(4), notifyWebhookCount.Load())
	require.Equal(t, int32(2), offlineWebhookCount.Load())

	conflictRequest := cloneMessageRequest(t, request)
	conflictRequest["payload"] = []byte(`{"type":1001,"content":"different"}`)
	status, conflict := postPersistMessage(t, httpAddr, conflictRequest)
	require.Equal(t, http.StatusConflict, status, conflict)
	require.Equal(t, "idempotency_conflict", conflict["code"])
	require.Equal(t, false, conflict["retryable"])

	messages, err = service.Store.LoadLastMsgs(fakeChannelID, wkproto.ChannelTypePerson, 10)
	require.NoError(t, err)
	require.Len(t, messages, 5)
}

func provisionTestDevice(t *testing.T, uid string) {
	t.Helper()
	now := time.Now()
	require.NoError(t, service.Store.AddDevice(wkdb.Device{
		Id:          service.Store.NextPrimaryKey(),
		Uid:         uid,
		DeviceFlag:  uint64(wkproto.APP),
		DeviceLevel: uint8(wkproto.DeviceLevelMaster),
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}))
}

func provisionPersonAllowlist(t *testing.T, ownerUID, memberUID string) {
	t.Helper()
	now := time.Now()
	require.NoError(t, service.Store.AddAllowlist(ownerUID, wkproto.ChannelTypePerson, []wkdb.Member{{
		Uid:       memberUID,
		CreatedAt: &now,
		UpdatedAt: &now,
	}}))
}

func waitForReceivedUser(t *testing.T, received <-chan string) string {
	t.Helper()
	select {
	case uid := <-received:
		return uid
	case <-time.After(3 * time.Second):
		t.Fatal("等待关系系统消息在线分发超时")
		return ""
	}
}

func waitForCounterAtLeast(t *testing.T, counter *atomic.Int32, want int32, label string) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		return counter.Load() >= want
	}, 3*time.Second, 10*time.Millisecond, "%s 计数未达到 %d，实际为 %d", label, want, counter.Load())
}

func reserveTestAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

func postPersistMessage(t *testing.T, httpAddr string, body map[string]any) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	client := &http.Client{Timeout: 5 * time.Second}
	var response *http.Response
	for attempt := 0; attempt < 20; attempt++ {
		request, requestErr := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/message/send", bytes.NewReader(data))
		require.NoError(t, requestErr)
		request.Header.Set("Content-Type", "application/json")
		response, err = client.Do(request)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, err)
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(responseBody, &decoded), fmt.Sprintf("body=%s", responseBody))
	return response.StatusCode, decoded
}

func cloneMessageRequest(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(source)
	require.NoError(t, err)
	var clone map[string]any
	require.NoError(t, json.Unmarshal(data, &clone))
	return clone
}
