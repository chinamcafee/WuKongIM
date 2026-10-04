package meta_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestProjectedEventPageOrdersBySequenceBeforeApplyingLimit(t *testing.T) {
	ctx := context.Background()
	db, err := meta.Open(filepath.Join(t.TempDir(), "meta"))
	require.NoError(t, err)
	defer db.Close()
	s := db.MetaDB().HashSlot(7)
	cursor := meta.MessageEventCursor{ChannelID: "group", ChannelType: 2, ClientMsgNo: "old-client", LastMsgEventSeq: 257}
	for _, row := range []struct {
		key string
		seq uint64
	}{{"a", 257}, {"z", 1}, {"m", 9}} {
		require.NoError(t, s.ImportMessageEventProjection(ctx, meta.MessageEventState{ChannelID: "group", ChannelType: 2, ClientMsgNo: "old-client", EventKey: row.key, Status: "closed", LastMsgEventSeq: row.seq, LastEventID: "event-" + row.key, LastEventType: "stream.close", LastVisibility: "public", SnapshotPayload: []byte(`{"text":"快照"}`)}, cursor))
	}
	q := meta.MessageEventSequenceQuery{ChannelID: "group", ChannelType: 2, ClientMsgNo: "old-client", Limit: 1}
	rows, err := s.ListMessageEventStatesBySequence(ctx, q)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "z", rows[0].EventKey)
	require.Equal(t, uint64(1), rows[0].LastMsgEventSeq)
	q.AfterSeq = 1
	rows, err = s.ListMessageEventStatesBySequence(ctx, q)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "m", rows[0].EventKey)
	q.EventKey = "a"
	rows, err = s.ListMessageEventStatesBySequence(ctx, q)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, uint64(257), rows[0].LastMsgEventSeq)
	q.AfterSeq = 257
	rows, err = s.ListMessageEventStatesBySequence(ctx, q)
	require.NoError(t, err)
	require.Empty(t, rows)
}
