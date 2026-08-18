package api

import (
	"context"
	"net/http"
	"net/http/httptest"
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

type recordingCMDSyncUsecase struct {
	syncQueries      []cmdsync.SyncQuery
	acks             []cmdsync.SyncAckCommand
	syncResult       cmdsync.SyncResult
	syncErr          error
	ackErr           error
	batchSyncQueries []cmdsync.BatchSyncQuery
	batchAcks        []cmdsync.BatchAckCommand
	batchSyncResult  cmdsync.BatchSyncResult
	batchSyncErr     error
	batchAckErr      error
}

func (r *recordingCMDSyncUsecase) BatchSync(_ context.Context, query cmdsync.BatchSyncQuery) (cmdsync.BatchSyncResult, error) {
	r.batchSyncQueries = append(r.batchSyncQueries, query)
	return r.batchSyncResult, r.batchSyncErr
}

func (r *recordingCMDSyncUsecase) BatchAck(_ context.Context, cmd cmdsync.BatchAckCommand) error {
	r.batchAcks = append(r.batchAcks, cmd)
	return r.batchAckErr
}

func (r *recordingCMDSyncUsecase) Sync(_ context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error) {
	r.syncQueries = append(r.syncQueries, query)
	return r.syncResult, r.syncErr
}

func (r *recordingCMDSyncUsecase) SyncAck(_ context.Context, cmd cmdsync.SyncAckCommand) error {
	r.acks = append(r.acks, cmd)
	return r.ackErr
}
