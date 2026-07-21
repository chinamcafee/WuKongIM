package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPSenderErrorsDoNotExposeConfiguredCredentials(t *testing.T) {
	const secret = "top-secret-password"
	sender := NewHTTPSender(HTTPSenderOptions{Addr: "http://user:" + secret + "@127.0.0.1:1/webhook", Timeout: time.Second})
	err := sender.Send(context.Background(), SendRequest{Event: EventMsgNotify, Body: []byte(`[]`)})
	if err == nil {
		t.Fatal("Send() error = nil, want connection failure")
	}
	if strings.Contains(err.Error(), "user") || strings.Contains(err.Error(), secret) {
		t.Fatalf("Send() error exposed configured credentials: %q", err)
	}
}

func TestHTTPSenderPostsEventQueryAndBody(t *testing.T) {
	var gotEvent string
	var gotBody string
	var gotMethod string
	var gotContentType string
	var gotToken string
	var gotID string
	var gotAttempt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEvent = r.URL.Query().Get("event")
		gotToken = r.URL.Query().Get("token")
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotID = r.Header.Get("X-WK-Webhook-ID")
		gotAttempt = r.Header.Get("X-WK-Webhook-Attempt")
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		gotBody = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := NewHTTPSender(HTTPSenderOptions{
		Addr:    server.URL + "/webhook?token=keep",
		Timeout: time.Second,
	})
	if err := sender.Send(context.Background(), SendRequest{ID: "wh_stable", Event: EventMsgNotify, Body: []byte(`[{"message_id":1}]`), Attempt: 2}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want %q", gotMethod, http.MethodPost)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotEvent != EventMsgNotify {
		t.Fatalf("event query = %q, want %q", gotEvent, EventMsgNotify)
	}
	if gotToken != "keep" {
		t.Fatalf("token query = %q, want keep", gotToken)
	}
	if gotID != "wh_stable" || gotAttempt != "2" {
		t.Fatalf("durable headers = %q/%q", gotID, gotAttempt)
	}
	if gotBody != `[{"message_id":1}]` {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestHTTPSenderAcceptsAny2xxAndRejectsNon2xx(t *testing.T) {
	status := http.StatusAccepted
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer server.Close()

	sender := NewHTTPSender(HTTPSenderOptions{Addr: server.URL, Timeout: time.Second})
	if err := sender.Send(context.Background(), SendRequest{Event: EventMsgNotify, Body: []byte(`[]`)}); err != nil {
		t.Fatalf("Send() error = %v, want 202 accepted", err)
	}
	status = http.StatusBadGateway
	if err := sender.Send(context.Background(), SendRequest{Event: EventMsgNotify, Body: []byte(`[]`)}); err == nil {
		t.Fatalf("Send() error = nil, want non-2xx rejection")
	}
}

func TestHTTPSenderTimeoutReturnsPromptly(t *testing.T) {
	release := make(chan struct{})
	released := false
	closeRelease := func() {
		if !released {
			close(release)
			released = true
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer closeRelease()

	sender := NewHTTPSender(HTTPSenderOptions{Addr: server.URL, Timeout: 30 * time.Millisecond})
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- sender.Send(context.Background(), SendRequest{Event: EventMsgNotify, Body: []byte(`[]`)})
	}()
	var err error
	select {
	case err = <-done:
		closeRelease()
	case <-time.After(500 * time.Millisecond):
		closeRelease()
		t.Fatalf("Send() did not return before timeout guard")
	}
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("Send() error = nil, want timeout error")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("Send() elapsed = %v, want prompt timeout", elapsed)
	}
}
