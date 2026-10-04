package message

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEventSyncPreservesOriginalFilteringAfterSequencePage(t *testing.T) {
	store := &sequenceEventStore{rows: []MessageEventState{
		{EventKey: "private", LastMsgEventSeq: 1, LastVisibility: VisibilityPrivate},
		{EventKey: "public", LastMsgEventSeq: 2, LastVisibility: "public", Status: "closed", EndReason: 1},
		{EventKey: "restricted", LastMsgEventSeq: 3, LastVisibility: VisibilityRestricted},
		{EventKey: "later", LastMsgEventSeq: 4, LastVisibility: "public", SnapshotPayload: []byte(`{"text":"later"}`)},
	}}
	app := New(Options{EventStore: store})
	q := MessageEventSyncQuery{ChannelID: "group", ChannelType: 2, ClientMsgNo: "original", Limit: 2}
	page, err := app.SyncMessageEvents(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, uint64(2), page.NextMsgEventSeq)
	require.False(t, page.More, "original v2 filters the bounded page rather than refilling across private lanes")
	require.Equal(t, EventTypeStreamSnapshot, page.Events[0].Type)
	require.JSONEq(t, `{"status":"closed","end_reason":1}`, string(page.Events[0].Payload))
	q.IncludePrivate = true
	page, err = app.SyncMessageEvents(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, uint64(1), page.Events[0].Seq)
	require.Equal(t, uint64(2), page.NextMsgEventSeq)
	require.True(t, page.More)
	q.FromMsgEventSeq = 3
	q.IncludePrivate = false
	page, err = app.SyncMessageEvents(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, uint64(4), page.NextMsgEventSeq)
	require.JSONEq(t, `{"text":"later"}`, string(page.Events[0].Payload))
}

type sequenceEventStore struct {
	recordingMessageEventStore
	rows []MessageEventState
}

func (s *sequenceEventStore) ListMessageEventStatesBySequence(_ context.Context, _ MessageEventMessageKey, after uint64, key string, limit int) ([]MessageEventState, error) {
	var rows []MessageEventState
	for _, row := range s.rows {
		if row.LastMsgEventSeq > after && (key == "" || key == row.EventKey) {
			rows = append(rows, row)
			if len(rows) == limit {
				break
			}
		}
	}
	return rows, nil
}
