package handler

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/persistreceipt"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/require"
)

const (
	testFakeChannelID = "user-a@user-b"
	testChannelType   = uint8(1)
)

func TestResolveIdempotentPersistMessagesRejectsMissingClientMsgNo(t *testing.T) {
	event := newPersistEvent(1, "", "user-a", []byte(`{"type":1001}`))
	persists := resolveIdempotentPersistMessages(
		testFakeChannelID,
		testChannelType,
		[]*eventbus.Event{event},
		func(string, uint8, string) (wkdb.Message, error) { return wkdb.EmptyMessage, wkdb.ErrNotFound },
		nil,
	)
	require.Empty(t, persists)
	require.Equal(t, wkproto.ReasonMsgKeyError, event.ReasonCode)
	require.Equal(t, persistreceipt.CodeClientMsgNoRequired, event.PersistErrorCode)
}

func TestResolveIdempotentPersistMessagesDeduplicatesSameBatch(t *testing.T) {
	first := newPersistEvent(11, "relation-event-1", "user-a", []byte(`{"type":1001}`))
	duplicate := newPersistEvent(12, "relation-event-1", "user-a", []byte(`{"type":1001}`))
	persists := resolveIdempotentPersistMessages(
		testFakeChannelID,
		testChannelType,
		[]*eventbus.Event{first, duplicate},
		func(string, uint8, string) (wkdb.Message, error) { return wkdb.EmptyMessage, wkdb.ErrNotFound },
		nil,
	)
	require.Len(t, persists, 1)
	require.Equal(t, int64(11), persists[0].MessageID)
	require.True(t, duplicate.Deduplicated)
	require.Equal(t, int64(11), duplicate.MessageId)
	require.Empty(t, duplicate.PersistErrorCode)
}

func TestResolveIdempotentPersistMessagesUsesPersistedReceiptAfterRestart(t *testing.T) {
	dir := t.TempDir()
	firstDB := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(dir), wkdb.WithShardNum(1)))
	require.NoError(t, firstDB.Open())

	originalEvent := newPersistEvent(101, "relation-event-restart", "user-a", []byte(`{"type":1001}`))
	original := toPersistMessage(testFakeChannelID, testChannelType, originalEvent)
	original.MessageSeq = 7
	require.NoError(t, firstDB.AppendMessages(testFakeChannelID, testChannelType, []wkdb.Message{original}))
	require.NoError(t, firstDB.Close())

	restartedDB := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(dir), wkdb.WithShardNum(1)))
	require.NoError(t, restartedDB.Open())
	t.Cleanup(func() { require.NoError(t, restartedDB.Close()) })

	retry := newPersistEvent(202, "relation-event-restart", "user-a", []byte(`{"type":1001}`))
	loaded, err := restartedDB.LoadMsgByClientMsgNo(testFakeChannelID, testChannelType, "relation-event-restart")
	require.NoError(t, err)
	require.True(t, sameIdempotentMessage(loaded, toPersistMessage(testFakeChannelID, testChannelType, retry)), "loaded=%+v", loaded)
	persists := resolveIdempotentPersistMessages(
		testFakeChannelID,
		testChannelType,
		[]*eventbus.Event{retry},
		restartedDB.LoadMsgByClientMsgNo,
		nil,
	)
	require.Empty(t, persists)
	require.True(t, retry.Deduplicated)
	require.Equal(t, int64(101), retry.MessageId)
	require.Equal(t, uint64(7), retry.MessageSeq)
}

func TestResolveIdempotentPersistMessagesRejectsConflictingContent(t *testing.T) {
	originalEvent := newPersistEvent(101, "relation-event-conflict", "user-a", []byte(`{"type":1001}`))
	original := toPersistMessage(testFakeChannelID, testChannelType, originalEvent)
	original.MessageSeq = 7

	tests := []struct {
		name   string
		mutate func(*eventbus.Event)
	}{
		{name: "from uid", mutate: func(event *eventbus.Event) { event.Conn.Uid = "user-b" }},
		{name: "payload", mutate: func(event *eventbus.Event) { event.Frame.(*wkproto.SendPacket).Payload = []byte(`{"type":1002}`) }},
		{name: "red dot header", mutate: func(event *eventbus.Event) { event.Frame.(*wkproto.SendPacket).RedDot = false }},
		{name: "sync once header", mutate: func(event *eventbus.Event) { event.Frame.(*wkproto.SendPacket).SyncOnce = true }},
		{name: "setting", mutate: func(event *eventbus.Event) { event.Frame.(*wkproto.SendPacket).Setting = wkproto.SettingReceiptEnabled }},
		{name: "expire", mutate: func(event *eventbus.Event) { event.Frame.(*wkproto.SendPacket).Expire = 60 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retry := newPersistEvent(202, "relation-event-conflict", "user-a", []byte(`{"type":1001}`))
			tt.mutate(retry)
			conflicts := 0
			persists := resolveIdempotentPersistMessages(
				testFakeChannelID,
				testChannelType,
				[]*eventbus.Event{retry},
				func(string, uint8, string) (wkdb.Message, error) { return original, nil },
				func(string) { conflicts++ },
			)
			require.Empty(t, persists)
			require.False(t, retry.Deduplicated)
			require.Equal(t, wkproto.ReasonMsgKeyError, retry.ReasonCode)
			require.Equal(t, persistreceipt.CodeIdempotencyConflict, retry.PersistErrorCode)
			require.Equal(t, 1, conflicts)
			require.Equal(t, int64(101), original.MessageID)
			require.Equal(t, uint32(7), original.MessageSeq)
		})
	}
}

func TestResolveIdempotentPersistMessagesSeparatesStorageFailure(t *testing.T) {
	event := newPersistEvent(1, "relation-event-error", "user-a", []byte(`{"type":1001}`))
	persists := resolveIdempotentPersistMessages(
		testFakeChannelID,
		testChannelType,
		[]*eventbus.Event{event},
		func(string, uint8, string) (wkdb.Message, error) {
			return wkdb.EmptyMessage, errors.New("storage unavailable")
		},
		nil,
	)
	require.Empty(t, persists)
	require.Equal(t, wkproto.ReasonSystemError, event.ReasonCode)
	require.Equal(t, persistreceipt.CodePersistFailed, event.PersistErrorCode)
}

func TestResolveIdempotentPersistMessagesPreservesChannelModes(t *testing.T) {
	tests := []struct {
		name        string
		channelID   string
		channelType uint8
		mutate      func(*eventbus.Event)
		wantPersist int
	}{
		{name: "person", channelID: testFakeChannelID, channelType: wkproto.ChannelTypePerson, wantPersist: 1},
		{name: "group", channelID: "group-1", channelType: wkproto.ChannelTypeGroup, wantPersist: 1},
		{name: "cmd", channelID: "group-1@cmd", channelType: wkproto.ChannelTypeGroup, wantPersist: 1},
		{
			name:        "stream continues through dedicated stream path",
			channelID:   "group-1",
			channelType: wkproto.ChannelTypeGroup,
			mutate:      func(event *eventbus.Event) { event.StreamNo = "stream-1" },
			wantPersist: 0,
		},
		{
			name:        "non persistent command",
			channelID:   "group-1@cmd",
			channelType: wkproto.ChannelTypeGroup,
			mutate:      func(event *eventbus.Event) { event.Frame.(*wkproto.SendPacket).NoPersist = true },
			wantPersist: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := newPersistEvent(1, "mode-regression-1", "user-a", []byte("payload"))
			if tt.mutate != nil {
				tt.mutate(event)
			}
			persists := resolveIdempotentPersistMessages(
				tt.channelID,
				tt.channelType,
				[]*eventbus.Event{event},
				func(string, uint8, string) (wkdb.Message, error) { return wkdb.EmptyMessage, wkdb.ErrNotFound },
				nil,
			)
			require.Len(t, persists, tt.wantPersist)
			if tt.wantPersist == 1 {
				require.Equal(t, tt.channelID, persists[0].ChannelID)
				require.Equal(t, tt.channelType, persists[0].ChannelType)
			}
		})
	}
}

func TestShortClientMsgNoHashDoesNotExposeOriginalKey(t *testing.T) {
	hashed := shortClientMsgNoHash("relation-event-sensitive-key")
	require.Len(t, hashed, 12)
	require.NotContains(t, hashed, "relation-event-sensitive-key")
}

func newPersistEvent(messageID int64, clientMsgNo, fromUID string, payload []byte) *eventbus.Event {
	return &eventbus.Event{
		Conn:       &eventbus.Conn{Uid: fromUID},
		MessageId:  messageID,
		ReasonCode: wkproto.ReasonSuccess,
		Frame: &wkproto.SendPacket{
			Framer: wkproto.Framer{
				NoPersist: false,
				RedDot:    true,
			},
			ClientMsgNo: clientMsgNo,
			ChannelID:   "user-b",
			ChannelType: testChannelType,
			Payload:     payload,
		},
	}
}
