package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagerWebhookOutboxReturnsOperationalSnapshot(t *testing.T) {
	createdAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	provider := &webhookOutboxProviderStub{snapshot: WebhookOutboxSnapshot{
		Backlog: 2, DeadLetterCount: 1, DeliveredTombstones: 9, LogicalBytes: 2048,
		OldestAge: "3m0s", RetryAttempts: 4, OldestCreatedAt: &createdAt,
		DeadLetters: []WebhookDeadLetter{{
			ID: "wh_dead", Event: "msg.notify", Items: 1, Attempt: 3,
			CreatedAt: createdAt, LastErrorCode: "WEBHOOK_SEND_FAILED",
		}},
	}}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer", Password: "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.webhook", Actions: []string{"r"}}},
		}}),
		WebhookOutbox: provider,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/webhooks/outbox", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !provider.snapshotCalled {
		t.Fatalf("status = %d called=%v body=%s", rec.Code, provider.snapshotCalled, rec.Body.String())
	}
	var response WebhookOutboxSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.DeadLetterCount != 1 || len(response.DeadLetters) != 1 || response.DeadLetters[0].ID != "wh_dead" {
		t.Fatalf("response = %#v", response)
	}
}

func TestManagerWebhookOutboxReplayRequiresWritePermissionAndExplicitIDs(t *testing.T) {
	provider := &webhookOutboxProviderStub{replayed: 1}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{Username: "viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.webhook", Actions: []string{"r"}}}},
			{Username: "operator", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.webhook", Actions: []string{"w"}}}},
		}),
		WebhookOutbox: provider,
	})
	body := []byte(`{"ids":["wh_dead","wh_missing"]}`)

	for _, test := range []struct {
		user string
		want int
	}{
		{user: "viewer", want: http.StatusForbidden},
		{user: "operator", want: http.StatusOK},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/webhooks/outbox/replay", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, test.user))
		srv.Engine().ServeHTTP(rec, req)
		if rec.Code != test.want {
			t.Fatalf("user=%s status=%d want=%d body=%s", test.user, rec.Code, test.want, rec.Body.String())
		}
	}
	if len(provider.replayIDs) != 2 || provider.replayIDs[0] != "wh_dead" {
		t.Fatalf("replay ids = %#v", provider.replayIDs)
	}
}

func TestManagerWebhookOutboxReplayRejectsDuplicateIDs(t *testing.T) {
	provider := &webhookOutboxProviderStub{}
	srv := New(Options{WebhookOutbox: provider})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/webhooks/outbox/replay", bytes.NewBufferString(`{"ids":["wh_1","wh_1"]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || len(provider.replayIDs) != 0 {
		t.Fatalf("status=%d ids=%#v body=%s", rec.Code, provider.replayIDs, rec.Body.String())
	}
}

func TestManagerWebhookConfigReturnsStartupSnapshot(t *testing.T) {
	expected := WebhookConfigSnapshot{
		Enabled:                   true,
		HTTPAddr:                  "http://127.0.0.1:19090/webhook",
		FocusEvents:               []string{"msg.notify", "msg.offline"},
		SupportedEvents:           []string{"msg.notify", "msg.offline", "user.onlinestatus"},
		QueueSize:                 1024,
		Workers:                   16,
		OnlineStatusBatchMaxItems: 512,
		OnlineStatusBatchMaxWait:  "2s",
		OfflineUIDBatchSize:       512,
		RequestTimeout:            "5s",
		RetryMaxAttempts:          3,
		OutboxDir:                 "/var/lib/wukongim/webhook-outbox",
		OutboxMaxEntries:          1000000,
		OutboxMaxBytes:            4294967296,
		OutboxDispatchBatchSize:   100,
		OutboxRetryBaseDelay:      "1s",
		OutboxRetryMaxDelay:       "5m0s",
		OutboxDeliveredRetention:  "168h0m0s",
		Source:                    "startup_config",
		RequiresRestart:           true,
	}
	var called bool
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.webhook",
				Actions:  []string{"r"},
			}},
		}}),
		WebhookConfig: webhookConfigProviderFunc(func(ctx context.Context) (WebhookConfigSnapshot, error) {
			called = true
			if ctx == nil {
				t.Fatalf("WebhookConfigSnapshot() ctx = nil")
			}
			return expected, nil
		}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !called {
		t.Fatalf("WebhookConfigSnapshot() was not called")
	}
	if !jsonEqual(rec.Body.String(), `{
		"enabled": true,
		"http_addr": "http://127.0.0.1:19090/webhook",
		"focus_events": ["msg.notify", "msg.offline"],
		"supported_events": ["msg.notify", "msg.offline", "user.onlinestatus"],
		"queue_size": 1024,
		"workers": 16,
		"online_status_batch_max_items": 512,
		"online_status_batch_max_wait": "2s",
		"offline_uid_batch_size": 512,
		"request_timeout": "5s",
		"retry_max_attempts": 3,
		"outbox_dir": "/var/lib/wukongim/webhook-outbox",
		"outbox_max_entries": 1000000,
		"outbox_max_bytes": 4294967296,
		"outbox_dispatch_batch_size": 100,
		"outbox_retry_base_delay": "1s",
		"outbox_retry_max_delay": "5m0s",
		"outbox_delivered_retention": "168h0m0s",
		"source": "startup_config",
		"requires_restart": true
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(body) != 20 {
		t.Fatalf("field count = %d, want 20: %s", len(body), rec.Body.String())
	}
}

func TestManagerWebhookConfigUnavailableWithoutProvider(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts Options
		body string
	}{
		{
			name: "nil provider",
			body: `{"error":"service_unavailable","message":"webhook config provider is not configured"}`,
		},
		{
			name: "provider error",
			opts: Options{
				WebhookConfig: webhookConfigProviderFunc(func(context.Context) (WebhookConfigSnapshot, error) {
					return WebhookConfigSnapshot{}, errors.New("snapshot failed")
				}),
			},
			body: `{"error":"service_unavailable","message":"webhook config unavailable"}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(tt.opts)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestManagerWebhookConfigRequiresWebhookReadPermission(t *testing.T) {
	var called bool
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		WebhookConfig: webhookConfigProviderFunc(func(context.Context) (WebhookConfigSnapshot, error) {
			called = true
			return WebhookConfigSnapshot{}, nil
		}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if called {
		t.Fatalf("WebhookConfigSnapshot() called without cluster.webhook:r")
	}
}

type webhookConfigProviderFunc func(context.Context) (WebhookConfigSnapshot, error)

func (f webhookConfigProviderFunc) WebhookConfigSnapshot(ctx context.Context) (WebhookConfigSnapshot, error) {
	return f(ctx)
}

type webhookOutboxProviderStub struct {
	snapshot       WebhookOutboxSnapshot
	snapshotErr    error
	snapshotCalled bool
	replayed       int
	replayErr      error
	replayIDs      []string
}

func (s *webhookOutboxProviderStub) WebhookOutboxSnapshot(context.Context) (WebhookOutboxSnapshot, error) {
	s.snapshotCalled = true
	return s.snapshot, s.snapshotErr
}

func (s *webhookOutboxProviderStub) ReplayWebhookDeadLetters(_ context.Context, ids []string) (WebhookOutboxReplayResponse, error) {
	s.replayIDs = append([]string(nil), ids...)
	return WebhookOutboxReplayResponse{Requested: len(ids), Replayed: s.replayed}, s.replayErr
}
