package sendauthorization

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCallbackSignsExactBodyAndDoesNotCacheAllowedResults(t *testing.T) {
	key := "fixture-personal-send-key-only-at-least32"
	client, err := New("http://127.0.0.1:8083/internal/im/personal-send-authorizations", key, time.Second, 1)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client.client.Transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		digest := sha256.Sum256(body)
		canonical := strings.Join([]string{"POST", r.URL.EscapedPath(), r.Header.Get("X-LinkU-Timestamp"), r.Header.Get("X-LinkU-Nonce"), hex.EncodeToString(digest[:])}, "\n")
		mac := hmac.New(sha256.New, []byte(key))
		_, _ = mac.Write([]byte(canonical))
		if r.Header.Get("X-LinkU-Signature") != hex.EncodeToString(mac.Sum(nil)) || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("request was not signed")
		}
		if string(body) != `{"senderUid":"sender","recipientUid":"receiver","recipientIsSystem":false}` {
			t.Fatal("request disclosed unexpected fields")
		}
		payload := `{"success":true,"code":"SUCCESS","data":{"allowed":true}}`
		if calls == 2 {
			payload = `{"success":true,"code":"SUCCESS","data":{"allowed":false}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})
	first, err := client.Authorize(context.Background(), "sender", "receiver", false)
	if err != nil || !first {
		t.Fatal("valid allow rejected")
	}
	second, err := client.Authorize(context.Background(), "sender", "receiver", false)
	if err != nil || second || calls != 2 {
		t.Fatal("stale positive admission reused")
	}
}

func TestCallbackRejectsPressureAndUnknownResponses(t *testing.T) {
	client, _ := New("http://127.0.0.1/check", "fixture-personal-send-key-only-at-least32", time.Second, 1)
	client.permits <- struct{}{}
	if allowed, err := client.Authorize(context.Background(), "a", "b", false); allowed || err == nil {
		t.Fatal("pressure allowed admission")
	}
	<-client.permits
	for _, body := range []string{`{}`, `{"success":true,"data":{}}`, `{"success":false,"data":{"allowed":true}}`, `{"success":true,"data":{"allowed":"true"}}`, strings.Repeat(" ", 4097)} {
		client.client.Transport = roundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if allowed, err := client.Authorize(context.Background(), "a", "b", false); allowed || err == nil {
			t.Fatal("unknown response allowed admission")
		}
	}
	for _, status := range []int{302, 401, 503} {
		client.client.Transport = roundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"allowed":true}}`))}, nil
		})
		if allowed, err := client.Authorize(context.Background(), "a", "b", false); allowed || err == nil {
			t.Fatal("failed status allowed admission")
		}
	}
}

func TestCallbackConfigurationRejectsUnprotectedRemoteAndIncompleteSettings(t *testing.T) {
	for _, endpoint := range []string{"http://remote.example/check", "https://user:password@example.com/check", "https://example.com/check?key=value", "https://example.com/check#fragment"} {
		if _, err := New(endpoint, strings.Repeat("k", 32), time.Second, 1); err == nil {
			t.Fatal("unsafe URL accepted")
		}
	}
	if _, err := New("https://example.com/check", "short", time.Second, 1); err == nil {
		t.Fatal("short key accepted")
	}
}
