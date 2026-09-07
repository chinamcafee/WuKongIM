//go:build e2e

package preparation_cmd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const fixtureSecret = "preparation-cmd-synthetic-secret-at-least-32-bytes"
const fixtureUID = "preparation-cmd-synthetic-user"

var nonceSequence atomic.Uint64

type ackCursor struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	ThroughSeq  uint64 `json:"through_seq"`
}

type commandBatch struct {
	BatchID     string      `json:"batch_id"`
	AckChannels []ackCursor `json:"ack_channels"`
	Messages    []struct {
		Header struct {
			NoPersist int `json:"no_persist"`
			RedDot    int `json:"red_dot"`
			SyncOnce  int `json:"sync_once"`
		} `json:"header"`
		FromUID     string `json:"from_uid"`
		ChannelID   string `json:"channel_id"`
		ChannelType uint8  `json:"channel_type"`
		ClientMsgNo string `json:"client_msg_no"`
		MessageID   string `json:"message_idstr"`
		MessageSeq  uint64 `json:"message_seq"`
		Payload     []byte `json:"payload"`
	} `json:"messages"`
}

func TestPreparationCommandsHaveIndependentDurableDeviceCursors(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_API_INTERNAL_CREDENTIAL_HMAC_SECRET": fixtureSecret,
		"WK_GATEWAY_TOKEN_AUTH_ENABLED":          "true",
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	items := make([]map[string]any, 0, 2)
	for _, flag := range []int{0, 2} {
		items = append(items, map[string]any{
			"uid": fixtureUID, "deviceFlag": flag, "credentialStatus": "ACTIVE",
			"token": fmt.Sprintf("synthetic-token-%d", flag), "credentialVersion": 7,
			"loginSessionId": sessionID(flag), "expiresAt": time.Now().Add(time.Hour).UnixMilli(),
			"operationId":   fmt.Sprintf("synthetic-login-%d", flag),
			"operationKind": "LOGIN_TAKEOVER", "replacementCause": "SAME_DEVICE_FAMILY_LOGIN",
		})
	}
	status, body := signedRequest(t, ctx, node, http.MethodPut, "/internal/v3/device-credentials:apply-batch", -1, map[string]any{"items": items})
	require.Equal(t, http.StatusOK, status, "%s", body)

	// Stable event and business revision remain opaque, lossless payload bytes.
	payload := []byte(`{"type":99,"cmd":"matchPrepareChanged","param":{"schemaVersion":1,"eventId":"mp_e2e_1","userCode":"preparation-cmd-synthetic-user","revision":"9007199254740993","reason":"CANCELLED","previous":null,"current":null,"occurredAt":"2026-09-06T00:00:00Z"}}`)
	firstID, firstSeq := sendCommand(t, ctx, node, "mp_e2e_1", payload)
	duplicateID, duplicateSeq := sendCommand(t, ctx, node, "mp_e2e_1", payload)
	require.Equal(t, firstID, duplicateID)
	require.Equal(t, firstSeq, duplicateSeq)
	mobile := syncEventually(t, ctx, node, 0, 1)
	desktop := syncEventually(t, ctx, node, 2, 1)
	for _, batch := range []commandBatch{mobile, desktop} {
		require.Len(t, batch.Messages, 1)
		require.Len(t, batch.AckChannels, 1)
		message := batch.Messages[0]
		require.Equal(t, payload, message.Payload)
		require.Equal(t, firstID, message.MessageID)
		require.Equal(t, "____system", message.FromUID)
		require.Equal(t, uint8(8), message.ChannelType)
		require.Regexp(t, `^[0-9]{1,20}$`, message.ChannelID)
		require.Equal(t, message.ChannelID+"____cmd", batch.AckChannels[0].ChannelID)
		require.Equal(t, uint8(8), batch.AckChannels[0].ChannelType)
		require.Equal(t, firstSeq, batch.AckChannels[0].ThroughSeq)
		require.Zero(t, message.Header.NoPersist)
		require.Zero(t, message.Header.RedDot)
		require.Equal(t, 1, message.Header.SyncOnce)
	}
	// Receipts are device-bound; one terminal cannot drain the other's batch.
	ack(t, ctx, node, 2, mobile, http.StatusBadRequest)
	require.NoError(t, node.Restart(node.Process.BinaryPath))
	require.NoError(t, suite.WaitHTTPReady(ctx, node.APIAddr(), "/readyz"), node.DumpDiagnostics())
	mobileReplay := syncEventually(t, ctx, node, 0, 1)
	desktopReplay := syncEventually(t, ctx, node, 2, 1)
	require.Equal(t, mobile.BatchID, mobileReplay.BatchID)
	require.Equal(t, desktop.BatchID, desktopReplay.BatchID)
	ack(t, ctx, node, 0, mobile, http.StatusOK)
	ack(t, ctx, node, 0, mobile, http.StatusOK) // Lost HTTP response can safely retry.
	syncEventually(t, ctx, node, 0, 0)
	syncEventually(t, ctx, node, 2, 1)
	ack(t, ctx, node, 2, desktop, http.StatusOK)
	syncEventually(t, ctx, node, 2, 0)

	// Reverse the order for the next event to prove isolation in both directions.
	sendCommand(t, ctx, node, "mp_e2e_2", []byte(`{"type":99,"cmd":"matchPrepareChanged","param":{"eventId":"mp_e2e_2"}}`))
	desktop = syncEventually(t, ctx, node, 2, 1)
	ack(t, ctx, node, 2, desktop, http.StatusOK)
	syncEventually(t, ctx, node, 2, 0)
	mobile = syncEventually(t, ctx, node, 0, 1)
	ack(t, ctx, node, 0, mobile, http.StatusOK)
	syncEventually(t, ctx, node, 0, 0)
	page, err := suite.PostConversationList(ctx, node.APIAddr(), fixtureUID, 10)
	require.NoError(t, err)
	require.Empty(t, page.Conversations, "silent commands must not create chat rows or unread badges")
	t.Log("standard binary: both device families sync independently; restart/replay, ACK isolation, deduplication and silent conversation isolation passed")
}

func sessionID(flag int) string { return fmt.Sprintf("synthetic-session-%d", flag) }

// signedRequest exercises the public service-to-service HMAC contract with fresh nonces.
func signedRequest(t *testing.T, ctx context.Context, node *suite.StartedNode, method, path string, flag int, value any) (int, []byte) {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, method, "http://"+node.APIAddr()+path, bytes.NewReader(body))
	require.NoError(t, err)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := fmt.Sprintf("preparation-e2e-%d", nonceSequence.Add(1))
	digest := sha256.Sum256(body)
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", method, path, timestamp, nonce, hex.EncodeToString(digest[:]))
	if flag >= 0 {
		session := sessionID(flag)
		canonical = fmt.Sprintf("command-v1\n%s\n%s\n%s\n%s\n%d:%s\n%d\n%d:%s\n7\n%s", method, path, timestamp, nonce, len(fixtureUID), fixtureUID, flag, len(session), session, hex.EncodeToString(digest[:]))
		req.Header.Set("X-LinkU-UID", fixtureUID)
		req.Header.Set("X-LinkU-Device-Flag", strconv.Itoa(flag))
		req.Header.Set("X-LinkU-Login-Session-ID", session)
		req.Header.Set("X-LinkU-Credential-Version", "7")
	}
	mac := hmac.New(sha256.New, []byte(fixtureSecret))
	_, err = mac.Write([]byte(canonical))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LinkU-Timestamp", timestamp)
	req.Header.Set("X-LinkU-Nonce", nonce)
	req.Header.Set("X-LinkU-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 128*1024))
	require.NoError(t, err)
	return response.StatusCode, data
}

func syncEventually(t *testing.T, ctx context.Context, node *suite.StartedNode, flag, count int) commandBatch {
	t.Helper()
	var batch commandBatch
	require.Eventually(t, func() bool {
		status, data := signedRequest(t, ctx, node, http.MethodPost, "/v3/message/commands/sync", flag, map[string]any{"limit": 20})
		require.Equal(t, http.StatusOK, status, "%s", data)
		batch = commandBatch{}
		require.NoError(t, json.Unmarshal(data, &batch))
		return len(batch.Messages) == count
	}, 10*time.Second, 100*time.Millisecond, "flag %d expected %d messages", flag, count)
	return batch
}

func ack(t *testing.T, ctx context.Context, node *suite.StartedNode, flag int, batch commandBatch, expected int) {
	t.Helper()
	status, data := signedRequest(t, ctx, node, http.MethodPost, "/v3/message/commands/ack", flag, map[string]any{"batch_id": batch.BatchID, "ack_channels": batch.AckChannels})
	require.Equal(t, expected, status, "%s", data)
}

func sendCommand(t *testing.T, ctx context.Context, node *suite.StartedNode, eventID string, payload []byte) (string, uint64) {
	t.Helper()
	body := map[string]any{
		"from_uid": "____system", "subscribers": []string{fixtureUID}, "client_msg_no": eventID,
		"header":  map[string]int{"no_persist": 0, "red_dot": 0, "sync_once": 1},
		"payload": payload, "wait_for_persist": 1, "persist_timeout_ms": 3000,
	}
	var result struct {
		MessageID  string `json:"message_id"`
		MessageSeq uint64 `json:"message_seq"`
		Status     int    `json:"status"`
	}
	for {
		_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/message/send", body, &result)
		if !suite.IsMessageSendRetryRequired(err) {
			require.NoError(t, err, node.DumpDiagnostics())
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	require.Equal(t, http.StatusOK, result.Status)
	require.NotEmpty(t, result.MessageID)
	require.NotZero(t, result.MessageSeq)
	return result.MessageID, result.MessageSeq
}
