package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
)

const commandTestSecret = "command-test-secret"

func TestV3CommandSyncReturnsBatchMessagesAndDerivesSignedPrincipal(t *testing.T) {
	batchID := strings.Repeat("a", 64)
	cmdSync := &recordingCMDSyncUsecase{batchSyncResult: cmdsync.BatchSyncResult{
		BatchID: batchID,
		Messages: []cmdsync.SyncedMessage{{
			MessageID: 99, MessageSeq: 7, FromUID: "____system", ChannelID: "source", ChannelType: 1,
			ClientMsgNo: "cmd-1", ServerTimestampMS: 123000, RedDot: true, SyncOnce: true, Payload: []byte("cmd"),
		}},
		AckCursors: []cmdsync.AckCursor{{CommandChannelID: "source____cmd", ChannelType: 1, ThroughSeq: 7}},
		More:       true,
	}}
	srv := New(Options{CMDSync: cmdSync, InternalCredentialHMACSecret: commandTestSecret})
	rec := httptest.NewRecorder()
	body := `{"limit":20}`
	req := signedCommandRequest(t, http.MethodPost, "/v3/message/commands/sync", body, "sync-1")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	want := `{"batch_id":"` + batchID + `","messages":[{"header":{"no_persist":0,"red_dot":1,"sync_once":1},"setting":0,"message_id":99,"message_idstr":"99","client_msg_no":"cmd-1","message_seq":7,"from_uid":"____system","channel_id":"source","channel_type":1,"expire":0,"timestamp":123,"payload":"Y21k"}],"ack_channels":[{"channel_id":"source____cmd","channel_type":1,"through_seq":7}],"more":1}`
	if !jsonEqual(rec.Body.String(), want) {
		t.Fatalf("body = %q, want %s", rec.Body.String(), want)
	}
	wantQuery := cmdsync.BatchSyncQuery{
		UID: "u1", DeviceFlag: 0, LoginSessionID: "session-1", CredentialVersion: 37, Limit: 20,
	}
	if len(cmdSync.batchSyncQueries) != 1 || cmdSync.batchSyncQueries[0] != wantQuery {
		t.Fatalf("queries = %#v, want signed-principal query %#v", cmdSync.batchSyncQueries, wantQuery)
	}
}

func TestV3CommandSyncReturnsCanonicalEmptyTerminalBatch(t *testing.T) {
	srv := New(Options{
		CMDSync:                      &recordingCMDSyncUsecase{batchSyncResult: cmdsync.BatchSyncResult{}},
		InternalCredentialHMACSecret: commandTestSecret,
	})
	rec := httptest.NewRecorder()
	req := signedCommandRequest(t, http.MethodPost, "/v3/message/commands/sync", `{"limit":20}`, "sync-empty")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"batch_id":"","messages":[],"ack_channels":[],"more":0}`) {
		t.Fatalf("body = %q, want canonical empty terminal batch", rec.Body.String())
	}
}

func TestV3CommandAckMapsSignedPrincipalAndExplicitBatch(t *testing.T) {
	batchID := strings.Repeat("b", 64)
	cmdSync := &recordingCMDSyncUsecase{}
	srv := New(Options{CMDSync: cmdSync, InternalCredentialHMACSecret: commandTestSecret})
	body := `{"batch_id":"` + batchID + `","ack_channels":[{"channel_id":"source____cmd","channel_type":1,"through_seq":7}]}`
	rec := httptest.NewRecorder()
	req := signedCommandRequest(t, http.MethodPost, "/v3/message/commands/ack", body, "ack-1")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !jsonEqual(rec.Body.String(), `{"status":200}`) {
		t.Fatalf("status/body = %d/%s, want success", rec.Code, rec.Body.String())
	}
	if len(cmdSync.batchAcks) != 1 {
		t.Fatalf("acks = %#v, want one", cmdSync.batchAcks)
	}
	got := cmdSync.batchAcks[0]
	if got.UID != "u1" || got.DeviceFlag != 0 || got.LoginSessionID != "session-1" || got.CredentialVersion != 37 ||
		got.BatchID != batchID || len(got.AckCursors) != 1 || got.AckCursors[0].ThroughSeq != 7 {
		t.Fatalf("ack = %#v, want mapped signed principal and explicit cursor", got)
	}
}

func TestV3CommandRoutesRejectUnsignedTamperedAndMalformedRequests(t *testing.T) {
	validBatch := strings.Repeat("a", 64)
	tests := []struct {
		name       string
		path       string
		body       string
		signedBody string
		want       int
	}{
		{name: "unsigned", path: "/v3/message/commands/sync", body: `{"limit":20}`, want: http.StatusUnauthorized},
		{name: "tampered", path: "/v3/message/commands/sync", body: `{"limit":21}`, signedBody: `{"limit":20}`, want: http.StatusUnauthorized},
		{name: "negative limit", path: "/v3/message/commands/sync", body: `{"limit":-1}`, want: http.StatusBadRequest},
		{name: "short batch", path: "/v3/message/commands/ack", body: `{"batch_id":"short","ack_channels":[]}`, want: http.StatusBadRequest},
		{name: "empty cursors", path: "/v3/message/commands/ack", body: `{"batch_id":"` + validBatch + `","ack_channels":[]}`, want: http.StatusBadRequest},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := New(Options{CMDSync: &recordingCMDSyncUsecase{}, InternalCredentialHMACSecret: commandTestSecret})
			rec := httptest.NewRecorder()
			var req *http.Request
			if test.name == "unsigned" {
				req = httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				signedBody := test.body
				if test.signedBody != "" {
					signedBody = test.signedBody
				}
				req = signedCommandRequest(t, http.MethodPost, test.path, signedBody, "reject-"+strconv.Itoa(i))
				if test.signedBody != "" {
					req.Body = ioNopCloser(test.body)
				}
			}
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), test.want)
			}
		})
	}
}

func signedCommandRequest(t *testing.T, method, path, body, nonce string) *http.Request {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	digest := sha256.Sum256([]byte(body))
	canonical := internalCommandCanonical(method, path, timestamp, nonce, "u1", "0", "session-1", "37", hex.EncodeToString(digest[:]))
	mac := hmac.New(sha256.New, []byte(commandTestSecret))
	_, _ = mac.Write([]byte(canonical))
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LinkU-Timestamp", timestamp)
	req.Header.Set("X-LinkU-Nonce", nonce)
	req.Header.Set("X-LinkU-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set(internalCommandUIDHeader, "u1")
	req.Header.Set(internalCommandDeviceFlagHeader, "0")
	req.Header.Set(internalCommandLoginSessionHeader, "session-1")
	req.Header.Set(internalCommandCredentialVersionHeader, "37")
	return req
}

func ioNopCloser(body string) *readCloser {
	return &readCloser{Reader: strings.NewReader(body)}
}

type readCloser struct {
	*strings.Reader
}

func (r *readCloser) Close() error { return nil }
