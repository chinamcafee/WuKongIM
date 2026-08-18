package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
)

type recordingDeviceCredentialUsecase struct {
	applied []userusecase.ApplyDeviceCredentialCommand
	revoked []userusecase.RevokeDeviceCredentialCommand
}

func (u *recordingDeviceCredentialUsecase) ApplyDeviceCredentials(_ context.Context, commands []userusecase.ApplyDeviceCredentialCommand) []userusecase.DeviceCredentialResult {
	u.applied = append(u.applied, commands...)
	return make([]userusecase.DeviceCredentialResult, len(commands))
}

func (u *recordingDeviceCredentialUsecase) RevokeDeviceCredentials(_ context.Context, commands []userusecase.RevokeDeviceCredentialCommand) []userusecase.DeviceCredentialResult {
	u.revoked = append(u.revoked, commands...)
	return make([]userusecase.DeviceCredentialResult, len(commands))
}

func TestInternalDeviceCredentialApplyRequiresHMACAndRejectsReplay(t *testing.T) {
	const secret = "test-internal-credential-secret-at-least-32-bytes"
	usecase := &recordingDeviceCredentialUsecase{}
	server := New(Options{
		DeviceCredentials: usecase, InternalCredentialHMACSecret: secret,
		InternalCredentialReplayWindow: time.Minute, InternalCredentialMaxBatchSize: 10,
	})
	body := `{"items":[{"uid":"u1","deviceFlag":0,"credentialStatus":"ACTIVE","token":"token-1","credentialVersion":7,"loginSessionId":"session-7","expiresAt":1999999999999,"operationId":"operation-7","operationKind":"LOGIN_TAKEOVER","replacementCause":"SAME_DEVICE_FAMILY_LOGIN"}]}`
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "nonce-1"
	path := "/internal/v3/device-credentials:apply-batch"
	signature := signCredentialRequest(secret, http.MethodPut, path, timestamp, nonce, body)

	first := credentialRequestRecorder(server, http.MethodPut, path, body, timestamp, nonce, signature)
	if first.Code != http.StatusOK || len(usecase.applied) != 1 {
		t.Fatalf("first apply status/body/calls = %d/%s/%#v, want 200 and one call", first.Code, first.Body.String(), usecase.applied)
	}
	if got := usecase.applied[0]; got.UID != "u1" || got.DeviceFlag != 0 || got.CredentialVersion != 7 || got.Token != "token-1" {
		t.Fatalf("apply command = %#v, want authenticated request mapping", got)
	}

	replay := credentialRequestRecorder(server, http.MethodPut, path, body, timestamp, nonce, signature)
	if replay.Code != http.StatusUnauthorized || len(usecase.applied) != 1 {
		t.Fatalf("replay status/calls = %d/%d, want 401/no second call", replay.Code, len(usecase.applied))
	}
}

func TestInternalDeviceCredentialHMACBindsMethodPathAndBody(t *testing.T) {
	const secret = "test-internal-credential-secret-at-least-32-bytes"
	usecase := &recordingDeviceCredentialUsecase{}
	server := New(Options{
		DeviceCredentials: usecase, InternalCredentialHMACSecret: secret,
		InternalCredentialReplayWindow: time.Minute, InternalCredentialMaxBatchSize: 10,
	})
	body := `{"items":[{"uid":"u1","deviceFlag":0,"credentialStatus":"ACTIVE","token":"token-1","credentialVersion":7,"loginSessionId":"session-7","expiresAt":1999999999999,"operationId":"operation-7","operationKind":"LOGIN_TAKEOVER","replacementCause":"SAME_DEVICE_FAMILY_LOGIN"}]}`
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	path := "/internal/v3/device-credentials:apply-batch"
	signature := signCredentialRequest(secret, http.MethodPut, path, timestamp, "nonce-bound", body)

	recorder := credentialRequestRecorder(server, http.MethodPut, path, strings.Replace(body, "token-1", "token-2", 1), timestamp, "nonce-bound", signature)
	if recorder.Code != http.StatusUnauthorized || len(usecase.applied) != 0 {
		t.Fatalf("tampered body status/calls = %d/%d, want 401/0", recorder.Code, len(usecase.applied))
	}
}

func TestInternalDeviceCredentialRevokeHasNoTokenAndMapsStableCause(t *testing.T) {
	const secret = "test-internal-credential-secret-at-least-32-bytes"
	usecase := &recordingDeviceCredentialUsecase{}
	server := New(Options{
		DeviceCredentials: usecase, InternalCredentialHMACSecret: secret,
		InternalCredentialReplayWindow: time.Minute, InternalCredentialMaxBatchSize: 10,
	})
	body := `{"items":[{"uid":"u1","deviceFlag":2,"credentialVersion":8,"loginSessionId":"desktop-session","operationId":"logout-8","terminationCause":"SESSION_LOGGED_OUT"}]}`
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	path := "/internal/v3/device-credentials:revoke-batch"
	signature := signCredentialRequest(secret, http.MethodPost, path, timestamp, "nonce-revoke", body)
	recorder := credentialRequestRecorder(server, http.MethodPost, path, body, timestamp, "nonce-revoke", signature)

	if recorder.Code != http.StatusOK || len(usecase.revoked) != 1 {
		t.Fatalf("revoke status/body/calls = %d/%s/%#v, want 200 and one call", recorder.Code, recorder.Body.String(), usecase.revoked)
	}
	if got := usecase.revoked[0]; got.DeviceFlag != 2 || got.CredentialVersion != 8 || got.TerminationCause != "SESSION_LOGGED_OUT" {
		t.Fatalf("revoke command = %#v, want versioned desktop tombstone", got)
	}
}

func credentialRequestRecorder(server *Server, method, path, body, timestamp, nonce, signature string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LinkU-Timestamp", timestamp)
	request.Header.Set("X-LinkU-Nonce", nonce)
	request.Header.Set("X-LinkU-Signature", signature)
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func signCredentialRequest(secret, method, path, timestamp, nonce, body string) string {
	bodyDigest := sha256.Sum256([]byte(body))
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", method, path, timestamp, nonce, hex.EncodeToString(bodyDigest[:]))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}
