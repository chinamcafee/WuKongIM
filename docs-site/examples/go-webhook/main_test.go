package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestEvaluate(t *testing.T) {
	for _, tc := range []struct {
		name        string
		payload     []byte
		allow       bool
		replacement string
	}{
		{"plain allow", []byte("hello"), true, ""},
		{"plain replace", []byte("[replace] hello"), true, "Reviewed: hello"},
		{"plain reject", []byte("[reject] hello"), false, ""},
		{"SDK text allow", []byte(`{"type":1,"content":"hello"}`), true, ""},
		{"SDK text replace", []byte(`{"type":1,"content":"[replace] hello","large_id":9007199254740993}`), true, `{"content":"Reviewed: hello","large_id":9007199254740993,"type":1}`},
		{"SDK text reject", []byte(`{"type":1,"content":"[reject] hello"}`), false, ""},
		{"custom message passthrough", []byte(`{"type":99,"content":"[reject] hello"}`), true, ""},
		{"binary passthrough", []byte{0xff, 0x00, 0x01}, true, ""},
		{"JSON array passthrough", []byte(`["[replace] hello"]`), true, ""},
		{"JSON null passthrough", []byte(`null`), true, ""},
		{"non-string content passthrough", []byte(`{"type":1,"content":42}`), true, ""},
		{"replacement too large", []byte("[replace]" + strings.Repeat("x", maxPayloadBytes-len("[replace]"))), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := bytes.Clone(tc.payload)
			for repeat := 0; repeat < 2; repeat++ {
				got := evaluate(tc.payload)
				if got.Allow != tc.allow || string(got.Payload) != tc.replacement {
					t.Fatalf("unexpected decision: %+v", got)
				}
				reason := uint8(0)
				if !tc.allow {
					reason = businessRejectCode
				}
				if got.ReasonCode != reason {
					t.Fatalf("reason=%d want=%d", got.ReasonCode, reason)
				}
				if !bytes.Equal(tc.payload, original) {
					t.Fatal("modified the callback input")
				}
			}
		})
	}
}

func TestHandlerResponseContract(t *testing.T) {
	handler := newHandler(log.New(io.Discard, "", 0))
	for _, tc := range []struct {
		payload string
		want    string
	}{
		{"hello", `{"allow":true}`},
		{"[replace] hello", `{"allow":true,"payload":"UmV2aWV3ZWQ6IGhlbGxv"}`},
		{"[reject] hello", `{"allow":false,"reason_code":200}`},
	} {
		request := validRequest([]byte(tc.payload))
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		// The same request/key must yield the same wire decision on a client retry.
		for repeat := 0; repeat < 2; repeat++ {
			response := invoke(handler, http.MethodPost, "/webhook?event=msg.before_send", "application/json; charset=utf-8", data)
			if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != tc.want {
				t.Fatalf("status=%d response=%s", response.Code, response.Body)
			}
			if response.Header().Get("Content-Type") != "application/json" {
				t.Fatal("missing JSON content type")
			}
		}
	}
}

func TestHandlerRejectsInvalidEnvelope(t *testing.T) {
	good, _ := json.Marshal(validRequest([]byte("hello")))
	largePayload, _ := json.Marshal(validRequest(bytes.Repeat([]byte("x"), maxPayloadBytes+1)))
	missingIdentity := validRequest([]byte("hello"))
	missingIdentity.FromUID = ""
	missingJSON, _ := json.Marshal(missingIdentity)
	for _, tc := range []struct {
		name, method, path, contentType string
		body                            []byte
		status                          int
	}{
		{"wrong method", "GET", "/webhook?event=msg.before_send", "application/json", good, 405},
		{"wrong event", "POST", "/webhook?event=msg.notify", "application/json", good, 400},
		{"wrong media", "POST", "/webhook?event=msg.before_send", "text/plain", good, 415},
		{"unknown path", "POST", "/other?event=msg.before_send", "application/json", good, 404},
		{"invalid base64", "POST", "/webhook?event=msg.before_send", "application/json", []byte(`{"payload":"!"}`), 400},
		{"missing identity", "POST", "/webhook?event=msg.before_send", "application/json", missingJSON, 400},
		{"oversized payload", "POST", "/webhook?event=msg.before_send", "application/json", largePayload, 400},
		{"extra JSON", "POST", "/webhook?event=msg.before_send", "application/json", append(bytes.Clone(good), []byte(` {}`)...), 400},
		{"oversized body", "POST", "/webhook?event=msg.before_send", "application/json", append(bytes.Clone(good), bytes.Repeat([]byte(" "), maxRequestBytes)...), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := invoke(newHandler(log.New(io.Discard, "", 0)), tc.method, tc.path, tc.contentType, tc.body)
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d", response.Code, tc.status)
			}
			if strings.Contains(response.Body.String(), "hello") {
				t.Fatal("echoed message content in an error")
			}
		})
	}
}

func TestHandlerLogsOnlyDecision(t *testing.T) {
	var logs bytes.Buffer
	request := validRequest([]byte("private-message"))
	request.FromUID = "private-user"
	request.ChannelID = "private-channel"
	request.ClientMsgNo = "private-key"
	data, _ := json.Marshal(request)
	response := invoke(newHandler(log.New(&logs, "", 0)), "POST", "/webhook?event=msg.before_send", "application/json", data)
	if response.Code != 200 || logs.String() != "decision=allow\n" {
		t.Fatal("logs must contain only the decision")
	}
}

func validRequest(payload []byte) beforeSendRequest {
	return beforeSendRequest{FromUID: "sender", ChannelID: "example-group", ChannelType: 2, ClientMsgNo: "example-1", Payload: payload}
}

func invoke(handler http.Handler, method, path, contentType string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestHandlerOverloadAndRecovery(t *testing.T) {
	handler := newHandler(log.New(io.Discard, "", 0))
	data, _ := json.Marshal(validRequest([]byte("hello")))
	entered := make(chan struct{}, maxInFlight)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var workers sync.WaitGroup
	defer func() { unblock(); workers.Wait() }()
	results := make(chan int, maxInFlight)
	for i := 0; i < maxInFlight; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			request := httptest.NewRequest("POST", "/webhook?event=msg.before_send", nil)
			request.Header.Set("Content-Type", "application/json")
			request.Body = io.NopCloser(&gatedReader{Reader: bytes.NewReader(data), entered: entered, release: release})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			results <- response.Code
		}()
	}
	for i := 0; i < maxInFlight; i++ {
		<-entered
	}
	excess := invoke(handler, "POST", "/webhook?event=msg.before_send", "application/json", data)
	if excess.Code != http.StatusServiceUnavailable {
		t.Errorf("overload status=%d", excess.Code)
	}
	unblock()
	for i := 0; i < maxInFlight; i++ {
		if code := <-results; code != http.StatusOK {
			t.Errorf("held request status=%d", code)
		}
	}
	recovered := invoke(handler, "POST", "/webhook?event=msg.before_send", "application/json", data)
	if recovered.Code != http.StatusOK {
		t.Fatalf("recovery status=%d", recovered.Code)
	}
}

type gatedReader struct {
	io.Reader
	once    sync.Once
	entered chan<- struct{}
	release <-chan struct{}
}

func (r *gatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { r.entered <- struct{}{}; <-r.release })
	return r.Reader.Read(p)
}

func TestRunRejectsNonLoopbackAddresses(t *testing.T) {
	for _, addr := range []string{":8090", "0.0.0.0:8090", "[::]:8090", "localhost:8090", "invalid"} {
		if err := run(context.Background(), addr, log.New(io.Discard, "", 0)); err == nil {
			t.Fatalf("accepted non-loopback IP address %q", addr)
		}
	}
}
