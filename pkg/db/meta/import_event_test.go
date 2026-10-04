package meta_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestImportedEventProjectionPreservesTerminalStateCursorAndExactRetry(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "meta")
	db, err := meta.Open(dir)
	require.NoError(t, err)
	s := db.MetaDB().HashSlot(7)
	state := meta.MessageEventState{ChannelID: "group", ChannelType: 2, ClientMsgNo: "old-client", EventKey: "main", Status: "closed", LastMsgEventSeq: 257, LastEventID: "old-close", LastEventType: "stream.close", LastVisibility: "public", LastOccurredAt: 1700000000000, SnapshotPayload: []byte(`{"kind":"text","text":"原始快照"}`), EndReason: 1}
	cursor := meta.MessageEventCursor{ChannelID: "group", ChannelType: 2, ClientMsgNo: "old-client", LastMsgEventSeq: 300}
	require.NoError(t, s.ImportMessageEventProjection(ctx, state, cursor))
	require.NoError(t, s.ImportMessageEventProjection(ctx, state, cursor))
	different := state
	different.SnapshotPayload = []byte("changed")
	require.ErrorIs(t, s.ImportMessageEventProjection(ctx, different, cursor), meta.ErrStaleMeta)
	require.NoError(t, db.Close())
	db, err = meta.Open(dir)
	require.NoError(t, err)
	defer db.Close()
	s = db.MetaDB().HashSlot(7)
	stored, ok, err := s.GetMessageEventState(ctx, "group", 2, "old-client", "main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, state, stored)
	retry, err := s.AppendMessageEvent(ctx, meta.MessageEventAppend{ChannelID: "group", ChannelType: 2, ClientMsgNo: "old-client", EventID: "old-close", EventKey: "main", EventType: "stream.close", Payload: []byte("different request payload")})
	require.NoError(t, err)
	require.Equal(t, uint64(257), retry.MsgEventSeq)
	require.Equal(t, state, retry.State)
	next, err := s.AppendMessageEvent(ctx, meta.MessageEventAppend{ChannelID: "group", ChannelType: 2, ClientMsgNo: "old-client", EventID: "new-lane", EventKey: "second", EventType: "stream.open", OccurredAt: 1700000000001, UpdatedAt: 1700000000002})
	require.NoError(t, err)
	require.Equal(t, uint64(301), next.MsgEventSeq)
	require.ErrorIs(t, s.ImportMessageEventProjection(ctx, state, cursor), meta.ErrStaleMeta)
}
