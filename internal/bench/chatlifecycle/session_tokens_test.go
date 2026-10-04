package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestEngineWorkerSessionPreparesCredentialBeforeReturningClient(t *testing.T) {
	calls := 0
	factory := engineWorkerSessionFactory{
		address: "127.0.0.1:1", ackTimeout: time.Second, runID: "run", assignmentID: "assignment",
		tokens: target.NewClient(target.Config{APIAddrs: []string{"http://target.test"}, Token: "bench-secret", HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if req.Method != http.MethodPost || req.URL.Path != "/bench/v1/users/tokens" || req.Header.Get("Authorization") != "Bearer bench-secret" {
				t.Fatal("unexpected token preparation request")
			}
			deadline, ok := req.Context().Deadline()
			if !ok || time.Until(deadline) > 5*time.Second {
				t.Fatal("token preparation must have a bounded deadline")
			}
			var body model.BatchTokensRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.RunID != "run" || body.BatchID != "assignment-token-u1" || !body.Upsert || len(body.Users) != 1 || body.Users[0] != (model.UserTokenItem{UID: "u1", Token: "connect-secret"}) {
				t.Fatal("incorrect credential preparation")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"accepted":1}`)), Header: make(http.Header)}, nil
		})}}),
	}
	// A returning login repeats the idempotent preparation; it does not depend on
	// a process-local cache surviving churn, cancellation, or another worker.
	for i := 1; i <= 2; i++ {
		client, err := factory.NewSession(context.Background(), "u1", "connect-secret")
		if err != nil || client == nil || calls != i {
			t.Fatalf("client construction did not follow credential persistence: calls=%d", calls)
		}
		_ = client.Close()
	}
}

func TestEngineWorkerSessionCannotConnectAfterTokenPreparationFailure(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		factory := engineWorkerSessionFactory{address: "127.0.0.1:1", ackTimeout: time.Second,
			tokens: target.NewClient(target.Config{APIAddrs: []string{"http://target.test"}, HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("private-token-value")), Header: make(http.Header)}, nil
			})}}),
		}
		client, err := factory.NewSession(context.Background(), "u1", "connect-secret")
		if client != nil || err == nil || strings.Contains(err.Error(), "private-token-value") {
			t.Fatal("failed persistence must withhold the client and redact private errors")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, err := (engineWorkerSessionFactory{}).NewSession(ctx, "u1", "connect-secret")
	if client != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled login must not prepare or create a client")
	}
}

func TestEngineWorkerSessionHonorsCancellationAfterTokenResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory := engineWorkerSessionFactory{address: "127.0.0.1:1", ackTimeout: time.Second,
		tokens: target.NewClient(target.Config{APIAddrs: []string{"http://target.test"}, HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(*http.Request) (*http.Response, error) {
			cancel()
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		})}}),
	}
	client, err := factory.NewSession(ctx, "u1", "connect-secret")
	if client != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled token preparation must not produce a client")
	}
}
