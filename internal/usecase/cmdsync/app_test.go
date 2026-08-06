package cmdsync

import (
	"context"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestSyncReadsCMDKindRowsAndStripsCommandSuffix(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2}] = []SyncedMessage{{
		MessageID: 1, MessageSeq: 3, ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, FromUID: "u2", Payload: []byte("cmd"),
	}}
	app := New(Options{States: store, Messages: store})

	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ChannelID != "g1" || got.Messages[0].MessageSeq != 3 {
		t.Fatalf("messages = %+v", got.Messages)
	}
}

func TestSyncAckAdvancesCMDKindReadSeqOnlyFromLatestGeneration(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 5, ChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store})

	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if err := app.SyncAck(context.Background(), SyncAckCommand{UID: "u1", LastMessageSeq: 5}); err != nil {
		t.Fatalf("SyncAck(): %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].Kind != metadb.ConversationKindCMD || store.upserts[0].ReadSeq != 5 {
		t.Fatalf("upserts = %+v", store.upserts)
	}
}

func TestSyncAckAdvancesSyncOnceSourceChannelRows(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: "g1", ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: "g1", ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 6, ChannelID: "g1", ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store})

	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if err := app.SyncAck(context.Background(), SyncAckCommand{UID: "u1"}); err != nil {
		t.Fatalf("SyncAck(): %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].ChannelID != "g1" || store.upserts[0].ReadSeq != 6 {
		t.Fatalf("upserts = %+v, want sync_once source channel read progress", store.upserts)
	}
}

func TestSyncUsesReadDeleteFloorAndSortsDeterministically(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{
		{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2, ReadSeq: 1, DeletedToSeq: 3, ActiveAt: 200},
		{UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2, ReadSeq: 0, ActiveAt: 100},
	}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2}] = []SyncedMessage{
		{MessageID: 12, MessageSeq: 4, ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2, ServerTimestampMS: 10},
		{MessageID: 13, MessageSeq: 5, ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2, ServerTimestampMS: 20},
	}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2}] = []SyncedMessage{{
		MessageID: 11, MessageSeq: 1, ChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2, ServerTimestampMS: 20,
	}}
	app := New(Options{States: store, Messages: store, DefaultLimit: 10, MaxLimit: 10})

	got, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("Sync(): %v", err)
	}
	if want := []messageLoadCall{
		{key: CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("b"), ChannelType: 2}, fromSeq: 4, limit: 11},
		{key: CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("a"), ChannelType: 2}, fromSeq: 1, limit: 11},
	}; !reflect.DeepEqual(store.messageCalls, want) {
		t.Fatalf("message calls = %#v, want %#v", store.messageCalls, want)
	}
	if gotIDs := syncMessageChannelIDs(got.Messages); !reflect.DeepEqual(gotIDs, []string{"b", "a", "b"}) {
		t.Fatalf("message channel IDs = %#v, want sorted stripped ids", gotIDs)
	}
}

func TestSyncRecordsLatestGenerationOnly(t *testing.T) {
	store := newCmdSyncStore()
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("old"), ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("old"), ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 2, ChannelID: runtimechannelid.ToCommandChannel("old"), ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store})
	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("first Sync(): %v", err)
	}

	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: runtimechannelid.ToCommandChannel("new"), ChannelType: 2, ActiveAt: 200,
	}}
	store.messages[CommandChannelKey{ChannelID: runtimechannelid.ToCommandChannel("new"), ChannelType: 2}] = []SyncedMessage{{
		MessageSeq: 9, ChannelID: runtimechannelid.ToCommandChannel("new"), ChannelType: 2,
	}}
	if _, err := app.Sync(context.Background(), SyncQuery{UID: "u1", Limit: 10}); err != nil {
		t.Fatalf("second Sync(): %v", err)
	}
	if err := app.SyncAck(context.Background(), SyncAckCommand{UID: "u1"}); err != nil {
		t.Fatalf("SyncAck(): %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].ChannelID != runtimechannelid.ToCommandChannel("new") || store.upserts[0].ReadSeq != 9 {
		t.Fatalf("upserts = %+v, want latest generation only", store.upserts)
	}
}

func TestBatchSyncReturnsExplicitCursorsAndMore(t *testing.T) {
	store := newCmdSyncStore()
	channelID := runtimechannelid.ToCommandChannel("g1")
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: channelID, ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: channelID, ChannelType: 2}] = []SyncedMessage{
		{MessageID: 1, MessageSeq: 1, ChannelID: channelID, ChannelType: 2, ServerTimestampMS: 1, RedDot: true},
		{MessageID: 2, MessageSeq: 2, ChannelID: channelID, ChannelType: 2, ServerTimestampMS: 2},
	}
	app := New(Options{States: store, Messages: store, DefaultLimit: 1, MaxLimit: 10})

	got, err := app.BatchSync(context.Background(), BatchSyncQuery{UID: "u1", Limit: 1})
	if err != nil {
		t.Fatalf("BatchSync(): %v", err)
	}
	if got.BatchID == "" || !got.More || len(got.Messages) != 1 || got.Messages[0].ChannelID != "g1" || !got.Messages[0].RedDot {
		t.Fatalf("batch = %#v, want one stripped red-dot message and more", got)
	}
	wantCursors := []AckCursor{{CommandChannelID: channelID, ChannelType: 2, ThroughSeq: 1}}
	if !reflect.DeepEqual(got.AckCursors, wantCursors) {
		t.Fatalf("ack cursors = %#v, want %#v", got.AckCursors, wantCursors)
	}
}

func TestBatchAckSurvivesAppRecreationAndIsMonotonic(t *testing.T) {
	store := newCmdSyncStore()
	channelID := runtimechannelid.ToCommandChannel("g1")
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: channelID, ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: channelID, ChannelType: 2}] = []SyncedMessage{{
		MessageID: 1, MessageSeq: 5, ChannelID: channelID, ChannelType: 2,
	}}
	firstApp := New(Options{States: store, Messages: store})
	batch, err := firstApp.BatchSync(context.Background(), BatchSyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("BatchSync(): %v", err)
	}

	recreatedApp := New(Options{States: store, Messages: store})
	if err := recreatedApp.BatchAck(context.Background(), BatchAckCommand{
		UID: "u1", BatchID: batch.BatchID, AckCursors: batch.AckCursors,
	}); err != nil {
		t.Fatalf("BatchAck() after app recreation: %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].ChannelID != channelID || store.upserts[0].ReadSeq != 5 {
		t.Fatalf("upserts = %#v, want command read seq 5", store.upserts)
	}
}

func TestBatchSyncReplayReturnsSameDeterministicBatch(t *testing.T) {
	store := newCmdSyncStore()
	channelID := runtimechannelid.ToCommandChannel("g1")
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: channelID, ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[CommandChannelKey{ChannelID: channelID, ChannelType: 2}] = []SyncedMessage{{
		MessageID: 1, MessageSeq: 5, ChannelID: channelID, ChannelType: 2, Payload: []byte("cmd"),
	}}
	app := New(Options{States: store, Messages: store})

	first, err := app.BatchSync(context.Background(), BatchSyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("first BatchSync(): %v", err)
	}
	second, err := app.BatchSync(context.Background(), BatchSyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("second BatchSync(): %v", err)
	}
	if first.BatchID == "" || first.BatchID != second.BatchID ||
		!reflect.DeepEqual(first.AckCursors, second.AckCursors) ||
		!reflect.DeepEqual(first.Messages, second.Messages) {
		t.Fatalf("replayed batches differ: first=%#v second=%#v", first, second)
	}
}

func TestBatchAckReplayAndNewerThenOlderAckRemainSafe(t *testing.T) {
	store := newCmdSyncStore()
	channelID := runtimechannelid.ToCommandChannel("g1")
	key := CommandChannelKey{ChannelID: channelID, ChannelType: 2}
	store.active = []metadb.ConversationState{{
		UID: "u1", Kind: metadb.ConversationKindCMD, ChannelID: channelID, ChannelType: 2, ActiveAt: 100,
	}}
	store.messages[key] = []SyncedMessage{{
		MessageID: 1, MessageSeq: 1, ChannelID: channelID, ChannelType: 2,
	}}
	app := New(Options{States: store, Messages: store})
	oldBatch, err := app.BatchSync(context.Background(), BatchSyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("old BatchSync(): %v", err)
	}
	store.messages[key] = append(store.messages[key], SyncedMessage{
		MessageID: 2, MessageSeq: 2, ChannelID: channelID, ChannelType: 2,
	})
	newBatch, err := app.BatchSync(context.Background(), BatchSyncQuery{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("new BatchSync(): %v", err)
	}

	for _, batch := range []BatchSyncResult{newBatch, newBatch, oldBatch} {
		if err := app.BatchAck(context.Background(), BatchAckCommand{
			UID: "u1", BatchID: batch.BatchID, AckCursors: batch.AckCursors,
		}); err != nil {
			t.Fatalf("BatchAck(%s): %v", batch.BatchID, err)
		}
	}
	if got := []uint64{
		store.upserts[0].ReadSeq,
		store.upserts[1].ReadSeq,
		store.upserts[2].ReadSeq,
	}; !reflect.DeepEqual(got, []uint64{2, 2, 1}) {
		t.Fatalf("explicit ACK writes = %v, want newer/replay/older cursors; metadata max merge prevents regression", got)
	}
}

func TestBatchAckRejectsTamperedOrDuplicateCursors(t *testing.T) {
	store := newCmdSyncStore()
	app := New(Options{States: store, Messages: store})
	cursors := []AckCursor{{CommandChannelID: runtimechannelid.ToCommandChannel("g1"), ChannelType: 2, ThroughSeq: 5}}
	records, err := syncRecordsFromAckCursors(cursors)
	if err != nil {
		t.Fatalf("syncRecordsFromAckCursors(): %v", err)
	}
	batchID := commandBatchID("u1", records)

	if err := app.BatchAck(context.Background(), BatchAckCommand{
		UID: "u1", BatchID: batchID, AckCursors: []AckCursor{{CommandChannelID: cursors[0].CommandChannelID, ChannelType: 2, ThroughSeq: 6}},
	}); err != ErrBatchIDMismatch {
		t.Fatalf("tampered BatchAck() error = %v, want %v", err, ErrBatchIDMismatch)
	}
	if err := app.BatchAck(context.Background(), BatchAckCommand{
		UID: "u1", BatchID: batchID, AckCursors: append(cursors, cursors[0]),
	}); err != ErrAckCursorInvalid {
		t.Fatalf("duplicate BatchAck() error = %v, want %v", err, ErrAckCursorInvalid)
	}
}

func TestSyncRejectsMissingDependencies(t *testing.T) {
	store := newCmdSyncStore()
	if _, err := New(Options{States: store, Messages: store}).Sync(context.Background(), SyncQuery{}); err != ErrUIDRequired {
		t.Fatalf("Sync() error = %v, want %v", err, ErrUIDRequired)
	}
	if err := New(Options{States: store}).SyncAck(context.Background(), SyncAckCommand{}); err != ErrUIDRequired {
		t.Fatalf("SyncAck() error = %v, want %v", err, ErrUIDRequired)
	}
	if _, err := New(Options{Messages: store}).Sync(context.Background(), SyncQuery{UID: "u1"}); err != ErrStateStoreRequired {
		t.Fatalf("Sync() error = %v, want %v", err, ErrStateStoreRequired)
	}
	if _, err := New(Options{States: store}).Sync(context.Background(), SyncQuery{UID: "u1"}); err != ErrMessageStoreRequired {
		t.Fatalf("Sync() error = %v, want %v", err, ErrMessageStoreRequired)
	}
	if err := New(Options{}).SyncAck(context.Background(), SyncAckCommand{UID: "u1"}); err != ErrStateStoreRequired {
		t.Fatalf("SyncAck() error = %v, want %v", err, ErrStateStoreRequired)
	}
}

type cmdSyncStore struct {
	active       []metadb.ConversationState
	upserts      []metadb.ConversationState
	messages     map[CommandChannelKey][]SyncedMessage
	messageCalls []messageLoadCall
}

func newCmdSyncStore() *cmdSyncStore {
	return &cmdSyncStore{messages: make(map[CommandChannelKey][]SyncedMessage)}
}

func (s *cmdSyncStore) ListConversationActiveView(_ context.Context, uid string, limit int) ([]metadb.ConversationState, error) {
	rows := make([]metadb.ConversationState, 0, len(s.active))
	for _, row := range s.active {
		if row.UID == uid && row.Kind == metadb.ConversationKindCMD {
			rows = append(rows, row)
		}
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *cmdSyncStore) UpsertConversationStates(_ context.Context, states []metadb.ConversationState) error {
	s.upserts = append(s.upserts, states...)
	return nil
}

type messageLoadCall struct {
	key     CommandChannelKey
	fromSeq uint64
	limit   int
}

func (s *cmdSyncStore) LoadCommandMessages(_ context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]SyncedMessage, error) {
	s.messageCalls = append(s.messageCalls, messageLoadCall{key: key, fromSeq: fromSeq, limit: limit})
	msgs := s.messages[key]
	out := make([]SyncedMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg.MessageSeq < fromSeq {
			continue
		}
		if msg.ChannelID == "" {
			msg.ChannelID = key.ChannelID
		}
		if msg.ChannelType == 0 {
			msg.ChannelType = key.ChannelType
		}
		out = append(out, msg)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func syncMessageChannelIDs(messages []SyncedMessage) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.ChannelID)
	}
	return out
}
