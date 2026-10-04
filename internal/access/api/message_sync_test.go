package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
)

func TestLegacyMessageSyncRoutesAreNotExposed(t *testing.T) {
	srv := New(Options{CMDSync: &recordingCMDSyncUsecase{}})
	for _, path := range []string{"/message/sync", "/message/syncack"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"uid":"u1"}`))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d body = %s, want 404", path, rec.Code, rec.Body.String())
		}
	}
}

func TestMessageCMDBindAndUnbindMapToUsecase(t *testing.T) {
	cmdSync := &recordingCMDSyncUsecase{}
	srv := New(Options{CMDSync: cmdSync})
	for _, test := range []struct {
		path string
		body string
	}{
		{path: "/message/cmd/bind", body: `{"uid":"u1","channel_id":"g1","channel_type":2}`},
		{path: "/message/cmd/unbind", body: `{"uid":"u1","channel_id":"g1","channel_type":2}`},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !jsonEqual(rec.Body.String(), `{"status":200}`) {
			t.Fatalf("%s status=%d body=%s", test.path, rec.Code, rec.Body.String())
		}
	}
	if got, want := cmdSync.binds, []cmdsync.BindCommand{{UID: "u1", ChannelID: "g1", ChannelType: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("binds = %#v, want %#v", got, want)
	}
	if got, want := cmdSync.unbinds, []cmdsync.UnbindCommand{{UID: "u1", ChannelID: "g1", ChannelType: 2}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unbinds = %#v, want %#v", got, want)
	}
}

type recordingCMDSyncUsecase struct {
	batchSyncQueries []cmdsync.BatchSyncQuery
	batchAcks        []cmdsync.BatchAckCommand
	batchSyncResult  cmdsync.BatchSyncResult
	batchSyncErr     error
	batchAckErr      error
	syncQueries      []cmdsync.SyncQuery
	acks             []cmdsync.SyncAckCommand
	binds            []cmdsync.BindCommand
	unbinds          []cmdsync.UnbindCommand
	syncResult       cmdsync.SyncResult
	syncErr          error
	ackErr           error
}

func (r *recordingCMDSyncUsecase) Bind(_ context.Context, cmd cmdsync.BindCommand) error {
	r.binds = append(r.binds, cmd)
	return nil
}

func (r *recordingCMDSyncUsecase) Unbind(_ context.Context, cmd cmdsync.UnbindCommand) error {
	r.unbinds = append(r.unbinds, cmd)
	return nil
}

func (r *recordingCMDSyncUsecase) Sync(_ context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error) {
	r.syncQueries = append(r.syncQueries, query)
	return r.syncResult, r.syncErr
}

func (r *recordingCMDSyncUsecase) SyncAck(_ context.Context, cmd cmdsync.SyncAckCommand) error {
	r.acks = append(r.acks, cmd)
	return r.ackErr
}

func TestMessageCMDBatchBindingMappingAndBodyLimit(t *testing.T) {
	for _, endpoint := range []string{"bind", "unbind"} {
		for _, body := range []string{`{"uids":["u1","u2"],"channel_id":"g","channel_type":2}`, `{"subscribers":["u2","u1"]}`} {
			usecase := &recordingCMDSyncUsecase{}
			srv := New(Options{CMDSync: usecase})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/message/cmd/"+endpoint, strings.NewReader(body)))
			if rec.Code != 200 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var uids, subscribers []string
			if endpoint == "bind" {
				uids = usecase.binds[0].UIDs
				subscribers = usecase.binds[0].Subscribers
			} else {
				uids = usecase.unbinds[0].UIDs
				subscribers = usecase.unbinds[0].Subscribers
			}
			if len(uids)+len(subscribers) != 2 {
				t.Fatalf("lost batch: %v %v", uids, subscribers)
			}
		}
		usecase := &recordingCMDSyncUsecase{}
		srv := New(Options{CMDSync: usecase})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/message/cmd/"+endpoint, strings.NewReader(`{"subscribers":["`+strings.Repeat("x", 256*1024)+`"]}`)))
		if rec.Code != 400 || len(usecase.binds)+len(usecase.unbinds) != 0 {
			t.Fatalf("oversized request reached usecase: %d", rec.Code)
		}
	}
}

func (r *recordingCMDSyncUsecase) BatchSync(_ context.Context, query cmdsync.BatchSyncQuery) (cmdsync.BatchSyncResult, error) {
	r.batchSyncQueries = append(r.batchSyncQueries, query)
	return r.batchSyncResult, r.batchSyncErr
}
func (r *recordingCMDSyncUsecase) BatchAck(_ context.Context, cmd cmdsync.BatchAckCommand) error {
	r.batchAcks = append(r.batchAcks, cmd)
	return r.batchAckErr
}
