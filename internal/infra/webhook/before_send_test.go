package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestBeforeSendClientRequestAndResponse(t *testing.T) {
	calls := 0
	client, err := NewBeforeSendClient("https://business.example/hook?tenant=one&event=old", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, EventBeforeSend, req.URL.Query().Get("event"))
		require.Equal(t, "one", req.URL.Query().Get("tenant"))
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		require.Equal(t, map[string]any{"from_uid": "u1", "channel_id": "g1", "channel_type": float64(2), "client_msg_no": "c1", "payload": "aGVsbG8=", "no_persist": true, "sync_once": false}, body)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"allow":true,"payload":"bmV3"}`)), Header: make(http.Header)}, nil
	}))
	require.NoError(t, err)
	decision, err := client.BeforeSend(context.Background(), message.BeforeSendRequest{FromUID: "u1", ChannelID: "g1", ChannelType: 2, ClientMsgNo: "c1", Payload: []byte("hello"), NoPersist: true})
	require.NoError(t, err)
	require.True(t, decision.Allow)
	require.Equal(t, "new", string(decision.Payload))
	require.Equal(t, 1, calls)
}

func TestBeforeSendClientRejectsInvalidResponsesAndDoesNotFollowRedirects(t *testing.T) {
	tests := []struct {
		name, body string
		status     int
	}{
		{"missing allow", `{}`, 200}, {"null allow", `{"allow":null}`, 200},
		{"wrong allow", `{"allow":"true"}`, 200}, {"empty", "", 200},
		{"unknown field", `{"allow":true,"from_uid":"attacker"}`, 200},
		{"trailing JSON", `{"allow":true}{}`, 200}, {"bad base64", `{"allow":true,"payload":"%%%"}`, 200},
		{"null payload", `{"allow":true,"payload":null}`, 200},
		{"duplicate allow", `{"allow":false,"allow":true}`, 200},
		{"oversize", strings.Repeat(" ", BeforeSendResponseMaxBytes+1), 200},
		{"redirect", `{"allow":true}`, 307}, {"HTTP failure", `{"allow":true}`, 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client, err := NewBeforeSendClient("http://business.example/hook", roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: http.Header{"Location": []string{"http://other.example/hook"}}}, nil
			}))
			require.NoError(t, err)
			_, err = client.BeforeSend(context.Background(), message.BeforeSendRequest{})
			require.Error(t, err)
			require.Equal(t, 1, calls)
		})
	}
}

func TestBeforeSendClientValidatesEndpoint(t *testing.T) {
	for _, addr := range []string{"", "/hook", "file:///hook", "https://", "https://user:pass@business.example", "https://business.example/#fragment"} {
		_, err := NewBeforeSendClient(addr, nil)
		require.Error(t, err, addr)
		require.NotContains(t, err.Error(), "user:pass")
	}
}

func TestBeforeSendClientPreservesAbsentAndEmptyPayload(t *testing.T) {
	for _, body := range []string{`{"allow":true}`, `{"allow":true,"payload":""}`, `{"allow":false,"reason_code":255}`} {
		client, err := NewBeforeSendClient("http://business.example", roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}))
		require.NoError(t, err)
		d, err := client.BeforeSend(context.Background(), message.BeforeSendRequest{})
		require.NoError(t, err)
		if strings.Contains(body, "payload") {
			require.NotNil(t, d.Payload)
		} else {
			require.Nil(t, d.Payload)
		}
		if !d.Allow {
			require.EqualValues(t, 255, d.ReasonCode)
		}
	}
}

func TestBeforeSendClientInvalidDenialFieldsRemainDenied(t *testing.T) {
	for _, body := range []string{`{"allow":false,"reason_code":-1}`, `{"allow":false,"reason_code":"wrong"}`, `{"allow":false,"reason_code":4294967296}`, `{"allow":false,"payload":{}}`} {
		client, err := NewBeforeSendClient("http://business.example", roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}))
		require.NoError(t, err)
		decision, err := client.BeforeSend(context.Background(), message.BeforeSendRequest{})
		require.NoError(t, err)
		require.False(t, decision.Allow)
		require.Zero(t, decision.ReasonCode)
	}
}
