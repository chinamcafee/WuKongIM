package message

import (
	"context"
	"errors"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSyncEmptyPersonConversationDoesNotReadMessages(t *testing.T) {
	reader := &recordingChannelMessageReader{}
	app := New(Options{Reader: reader, Memberships: &recordingSyncMembershipStore{}})
	query := SyncChannelMessagesQuery{LoginUID: "alice", ChannelID: "bob", ChannelType: channelTypePerson}
	page, err := app.SyncChannelMessages(context.Background(), query)
	if err != nil || page.Messages == nil || len(page.Messages) != 0 || page.More {
		t.Fatalf("empty person sync = %+v, %v", page, err)
	}
	batch, err := app.SyncChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: query.LoginUID, Items: []SyncChannelMessagesQuery{query}})
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Err != nil || batch.Items[0].Result.Messages == nil || len(batch.Items[0].Result.Messages) != 0 || batch.Items[0].Result.More {
		t.Fatalf("empty person batch = %+v, %v", batch, err)
	}
	if len(reader.queries) != 0 || reader.batchCalls != 0 {
		t.Fatalf("empty person sync read messages: single=%d batch=%d", len(reader.queries), reader.batchCalls)
	}
}

func TestSyncEmptyPersonBatchPreservesAlignment(t *testing.T) {
	reader := &recordingChannelMessageReader{batchResults: []ChannelMessageReadResult{{Page: ChannelMessagePage{Messages: []SyncedMessage{{MessageSeq: 7}}, HasMore: true}}}}
	memberships := &multiSyncMembershipStore{rows: map[ChannelID]metadb.UserChannelMembership{{ID: "group", Type: 2}: {JoinSeq: 5}}}
	app := New(Options{Reader: reader, Memberships: memberships})
	result, err := app.SyncChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: "alice", Items: []SyncChannelMessagesQuery{
		{ChannelID: "bob", ChannelType: channelTypePerson},
		{ChannelID: "group", ChannelType: 2},
		{ChannelID: "carol", ChannelType: channelTypePerson},
	}})
	if err != nil || len(result.Items) != 3 {
		t.Fatalf("batch = %+v, %v", result, err)
	}
	if reader.batchCalls != 1 || len(reader.batchQueries) != 1 || reader.batchQueries[0].ChannelID.ID != "group" || reader.batchQueries[0].MinSeq != 5 {
		t.Fatalf("batch reads = %+v, want only live group with visibility floor", reader.batchQueries)
	}
	for i, id := range []string{"bob", "group", "carol"} {
		item := result.Items[i]
		if item.ChannelID != id || item.Err != nil || item.Result.Messages == nil {
			t.Fatalf("item %d = %+v", i, item)
		}
		if i == 1 {
			if len(item.Result.Messages) != 1 || item.Result.Messages[0].MessageSeq != 7 || !item.Result.More {
				t.Fatalf("live group = %+v", item)
			}
		} else if len(item.Result.Messages) != 0 || item.Result.More {
			t.Fatalf("empty person = %+v", item)
		}
	}
}

func TestSyncEmptyConversationKeepsMembershipFences(t *testing.T) {
	for _, test := range []struct {
		name             string
		channelType      uint8
		found, tombstone bool
	}{
		{"missing group", 2, false, false},
		{"removed person", channelTypePerson, true, true},
		{"removed group", 2, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &recordingChannelMessageReader{}
			app := New(Options{Reader: reader, Memberships: &recordingSyncMembershipStore{ok: test.found, row: metadb.UserChannelMembership{Tombstone: test.tombstone}}})
			query := SyncChannelMessagesQuery{LoginUID: "alice", ChannelID: "peer", ChannelType: test.channelType}
			_, err := app.SyncChannelMessages(context.Background(), query)
			if !errors.Is(err, ErrSyncMembershipRequired) {
				t.Fatalf("single error = %v", err)
			}
			_, err = app.SyncChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: "alice", Items: []SyncChannelMessagesQuery{query}})
			if !errors.Is(err, ErrSyncMembershipRequired) {
				t.Fatalf("batch error = %v", err)
			}
			if len(reader.queries) != 0 || reader.batchCalls != 0 {
				t.Fatal("unauthorized history read")
			}
		})
	}
}

func TestSyncMissingChannelErrorsReturnEmptyPages(t *testing.T) {
	for _, cause := range []error{metadb.ErrNotFound, fmt.Errorf("routed read: %w", ErrChannelNotFound)} {
		t.Run(cause.Error(), func(t *testing.T) {
			reader := &recordingChannelMessageReader{err: cause}
			app := New(Options{Reader: reader, Memberships: liveSyncMembershipStore()})
			query := SyncChannelMessagesQuery{LoginUID: "alice", ChannelID: "group", ChannelType: 2}
			result, err := app.SyncChannelMessages(context.Background(), query)
			if err != nil || result.Messages == nil || len(result.Messages) != 0 || result.More {
				t.Fatalf("single = %+v, %v", result, err)
			}
			reader.err = nil
			reader.batchResults = []ChannelMessageReadResult{{Err: cause}}
			batch, err := app.SyncChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: "alice", Items: []SyncChannelMessagesQuery{query}})
			if err != nil || len(batch.Items) != 1 || batch.Items[0].Err != nil || batch.Items[0].Result.Messages == nil || len(batch.Items[0].Result.Messages) != 0 || batch.Items[0].Result.More {
				t.Fatalf("batch = %+v, %v", batch, err)
			}
		})
	}
}

func TestSyncEmptyPersonConversationPropagatesMembershipFailures(t *testing.T) {
	for _, cause := range []error{metadb.ErrNotFound, context.Canceled, ErrRouteNotReady} {
		reader := &recordingChannelMessageReader{}
		app := New(Options{Reader: reader, Memberships: &recordingSyncMembershipStore{err: cause}})
		query := SyncChannelMessagesQuery{LoginUID: "alice", ChannelID: "bob", ChannelType: channelTypePerson}
		if _, err := app.SyncChannelMessages(context.Background(), query); !errors.Is(err, cause) {
			t.Fatalf("single error=%v, want %v", err, cause)
		}
		if _, err := app.SyncChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: "alice", Items: []SyncChannelMessagesQuery{query}}); !errors.Is(err, cause) {
			t.Fatalf("batch error=%v, want %v", err, cause)
		}
		if len(reader.queries) != 0 || reader.batchCalls != 0 {
			t.Fatal("failed membership check read history")
		}
	}
}
