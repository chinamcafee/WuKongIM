package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
)

func TestV3CommandSyncReturnsBatchMessagesAndExplicitAckChannels(t *testing.T) {
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
	srv := New(Options{CMDSync: cmdSync})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v3/message/commands/sync", bytes.NewBufferString(`{"uid":"u1","limit":20}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	want := `{"batch_id":"` + batchID + `","messages":[{"header":{"no_persist":0,"red_dot":1,"sync_once":1},"setting":0,"message_id":99,"message_idstr":"99","client_msg_no":"cmd-1","message_seq":7,"from_uid":"____system","channel_id":"source","channel_type":1,"expire":0,"timestamp":123,"payload":"Y21k"}],"ack_channels":[{"channel_id":"source____cmd","channel_type":1,"through_seq":7}],"more":1}`
	if !jsonEqual(rec.Body.String(), want) {
		t.Fatalf("body = %q, want %s", rec.Body.String(), want)
	}
	if len(cmdSync.batchSyncQueries) != 1 || cmdSync.batchSyncQueries[0] != (cmdsync.BatchSyncQuery{UID: "u1", Limit: 20}) {
		t.Fatalf("queries = %#v, want mapped v3 query", cmdSync.batchSyncQueries)
	}
}

func TestV3CommandSyncReturnsCanonicalEmptyTerminalBatch(t *testing.T) {
	srv := New(Options{CMDSync: &recordingCMDSyncUsecase{
		batchSyncResult: cmdsync.BatchSyncResult{},
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v3/message/commands/sync", bytes.NewBufferString(`{"uid":"u1","limit":20}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"batch_id":"","messages":[],"ack_channels":[],"more":0}`) {
		t.Fatalf("body = %q, want canonical empty terminal batch", rec.Body.String())
	}
}

func TestV3CommandAckMapsExplicitBatch(t *testing.T) {
	batchID := strings.Repeat("b", 64)
	cmdSync := &recordingCMDSyncUsecase{}
	srv := New(Options{CMDSync: cmdSync})
	rec := httptest.NewRecorder()
	body := `{"uid":"u1","batch_id":"` + batchID + `","ack_channels":[{"channel_id":"source____cmd","channel_type":1,"through_seq":7}]}`
	req := httptest.NewRequest(http.MethodPost, "/v3/message/commands/ack", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !jsonEqual(rec.Body.String(), `{"status":200}`) {
		t.Fatalf("status/body = %d/%s, want success", rec.Code, rec.Body.String())
	}
	if len(cmdSync.batchAcks) != 1 {
		t.Fatalf("acks = %#v, want one", cmdSync.batchAcks)
	}
	got := cmdSync.batchAcks[0]
	if got.UID != "u1" || got.BatchID != batchID || len(got.AckCursors) != 1 || got.AckCursors[0].ThroughSeq != 7 {
		t.Fatalf("ack = %#v, want mapped explicit cursor", got)
	}
}

func TestV3CommandRoutesRejectMalformedRequests(t *testing.T) {
	for _, test := range []struct {
		path string
		body string
	}{
		{path: "/v3/message/commands/sync", body: `{"uid":"u1","limit":-1}`},
		{path: "/v3/message/commands/ack", body: `{"uid":"u1","batch_id":"short","ack_channels":[]}`},
		{path: "/v3/message/commands/ack", body: `{"uid":"","batch_id":"` + strings.Repeat("a", 64) + `","ack_channels":[{"channel_id":"c","channel_type":1,"through_seq":1}]}`},
	} {
		t.Run(test.path+test.body, func(t *testing.T) {
			srv := New(Options{CMDSync: &recordingCMDSyncUsecase{}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
			req.Header.Set("Content-Type", "application/json")
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
}
