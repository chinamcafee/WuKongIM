//go:build e2e

package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// TestGoBeforeSendWebhookExample proves the published business server works with
// real Product HTTP sends and committed history in a single-node cluster.
func TestGoBeforeSendWebhookExample(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	binary := filepath.Join(t.TempDir(), "go-webhook")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, ".")
	build.Dir = filepath.Join(root, "docs-site/examples/go-webhook")
	build.Env = append(os.Environ(), "GOWORK=off")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build example: %s", output)

	addr := suite.ReserveLoopbackPorts(t).APIAddr
	logFile, err := os.Create(filepath.Join(t.TempDir(), "callback.log"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logFile.Close()) })
	command := exec.Command(binary, "-addr", addr)
	command.Stdout = logFile
	command.Stderr = logFile
	suite.PrepareCommandProcessTree(command)
	require.NoError(t, command.Start())
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, suite.ReapCommandProcessTree(command.Process, 5*time.Second))
		select {
		case exitErr := <-done:
			require.NoError(t, exitErr, "example must stop gracefully")
		case <-stopCtx.Done():
			t.Error("example did not exit")
		}
	})
	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		response, err := client.Get("http://" + addr + "/healthz")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond, "example health endpoint did not become ready")

	cluster := suite.New(t).StartStaticCluster(1, suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_GATEWAY_TOKEN_AUTH_ON":         "true",
		"WK_CLUSTER_HASH_SLOT_COUNT":       "256",
		"WK_WEBHOOK_BEFORE_SEND_ENABLED":   "true",
		"WK_WEBHOOK_BEFORE_SEND_HTTP_ADDR": "http://" + addr + "/webhook",
		"WK_WEBHOOK_BEFORE_SEND_ON_ERROR":  "allow",
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitHTTPReady(ctx))
	node := cluster.MustNode(1)
	const sender, receiver, group = "example-sender", "example-receiver", "webhook-example-group"
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id": group, "channel_type": 2, "reset": 1, "subscribers": []string{sender, receiver},
	}))
	expected := map[string]string{}
	for _, tc := range []struct {
		id, text, want string
		reason         uint8
	}{
		{"example-allow", "hello", "hello", 1},
		{"example-replace", "[replace] hello", "Reviewed: hello", 1},
		{"example-reject", "[reject] hello", "", 200},
	} {
		payload, err := json.Marshal(map[string]any{"type": 1, "content": tc.text, "extra": "preserved"})
		require.NoError(t, err)
		result, err := suite.PostMessageSendEventually(ctx, node.APIAddr(), map[string]any{
			"from_uid": sender, "channel_id": group, "channel_type": 2, "client_msg_no": tc.id, "payload": payload,
		})
		require.NoError(t, err)
		require.Equal(t, tc.reason, result.Reason)
		if tc.reason == 1 {
			require.NotZero(t, result.MessageSeq)
			expected[tc.id] = tc.want
		} else {
			require.Zero(t, result.MessageSeq)
			require.Zero(t, result.MessageID)
		}
	}
	var history struct {
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
			Payload     []byte `json:"payload"`
		} `json:"messages"`
	}
	_, err = suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{
		"login_uid": receiver, "channel_id": group, "channel_type": 2, "limit": 10,
	}, &history)
	require.NoError(t, err)
	require.Len(t, history.Messages, 2)
	for _, message := range history.Messages {
		require.Contains(t, expected, message.ClientMsgNo)
		var payload struct {
			Type    int    `json:"type"`
			Content string `json:"content"`
			Extra   string `json:"extra"`
		}
		require.NoError(t, json.Unmarshal(message.Payload, &payload))
		require.Equal(t, 1, payload.Type)
		require.Equal(t, expected[message.ClientMsgNo], payload.Content)
		require.Equal(t, "preserved", payload.Extra)
		delete(expected, message.ClientMsgNo)
	}
	require.Empty(t, expected)
}
