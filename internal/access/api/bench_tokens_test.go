package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
)

func TestBenchTokensPersistThroughUserUsecase(t *testing.T) {
	users := &recordingUserUsecase{}
	srv := New(Options{BenchEnabled: true, BenchToken: "bench-secret", Users: users})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bench/v1/users/tokens", strings.NewReader(`{"run_id":"run","batch_id":"batch","upsert":true,"users":[{"uid":"u1","token":"t1","device_flag":0,"device_level":1},{"uid":"u2","token":"t2","device_flag":1}]}`))
	req.Header.Set("Authorization", "Bearer bench-secret")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(users.tokenCommands) != 2 {
		t.Fatalf("status=%d persisted=%d; want 200 and 2", rec.Code, len(users.tokenCommands))
	}
	for i, want := range []userusecase.UpdateTokenCommand{{UID: "u1", Token: "t1", DeviceFlag: 0, DeviceLevel: 1}, {UID: "u2", Token: "t2", DeviceFlag: 1}} {
		if users.tokenCommands[i] != want {
			t.Fatalf("token command %d mismatch", i)
		}
	}
}

func TestBenchTokensRejectUnavailableOrFailedPersistence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		users  UserUsecase
		status int
	}{
		{"missing", nil, http.StatusNotImplemented},
		{"failed", &recordingUserUsecase{err: errors.New("private-token-value")}, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(Options{BenchEnabled: true, Users: tc.users})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/bench/v1/users/tokens", strings.NewReader(`{"run_id":"run","batch_id":"batch","users":[{"uid":"u1","token":"t1"}]}`)))
			if rec.Code != tc.status || strings.Contains(rec.Body.String(), "private-token-value") {
				t.Fatalf("status=%d; want %d without private error", rec.Code, tc.status)
			}
		})
	}
}
