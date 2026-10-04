package migrationv2_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func TestStoppedOriginalSourceSelectionPreservesHistoryAndRecoversOnlyAbsentConversations(t *testing.T) {
	ctx := context.Background()
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	spool, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "original-selection", 128<<20)
	require.NoError(t, err)
	defer spool.Close()
	reader := migrationv2.Reader{}
	capture, err := migration.CaptureSources(ctx, []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: dir, ShardCount: 2}}}, reader, spool, nil)
	require.NoError(t, err)
	catalog, err := migration.BuildSourceCatalog(ctx, capture, spool, reader)
	require.NoError(t, err)
	selected, err := migration.SelectSources(ctx, capture, catalog, spool, reader, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(4), selected.Tables["Message"])
	var messages []uint64
	subscribers := []string{}
	var bobRead, bobDeleted, pendingRead uint64
	require.NoError(t, migration.WalkSelectedSources(ctx, spool, func(record migration.SelectedRecord) error {
		require.Equal(t, uint64(1), record.NodeID)
		switch record.Row.Table {
		case "Subscriber":
			facts, err := reader.DecodeBusiness(record.Row, record.Identity)
			if err != nil {
				return err
			}
			require.Equal(t, "migrationgroup", facts.Member.Channel.ID)
			subscribers = append(subscribers, facts.Member.UID)
		case "Message":
			if record.Row.Kind != migration.Primary {
				facts, err := reader.DecodeBusiness(record.Row, record.Identity)
				if err != nil {
					return err
				}
				if facts.Tail.Channel.ID == "migrationgroup" {
					require.Equal(t, uint64(3), facts.Tail.LastSeq)
				} else {
					require.Equal(t, uint64(1), facts.Tail.LastSeq)
				}
				return nil
			}
			facts, err := reader.DecodeBusiness(record.Row, record.Identity)
			if err != nil {
				return err
			}
			messages = append(messages, facts.Message.MessageID)
		case "MessageEventSeq":
			facts, err := reader.DecodeBusiness(record.Row, record.Identity)
			if err != nil {
				return err
			}
			require.Equal(t, uint64(2), facts.EventCursor.LastMsgEventSeq)
			require.Equal(t, "original-v2-0", facts.EventCursor.ClientMsgNo)
		case "Conversation":
			facts, err := reader.DecodeBusiness(record.Row, record.Identity)
			if err != nil {
				return err
			}
			require.NotNil(t, facts.Conversation)
			if record.Identity.UID == "migrationbob" && record.Identity.Channel.ID == "migrationgroup" {
				bobRead = facts.Conversation.ReadSeq
				bobDeleted = facts.Conversation.DeletedToSeq
			}
		case "PendingConversation":
			facts, err := reader.DecodeBusiness(record.Row, record.Identity)
			if err != nil {
				return err
			}
			require.True(t, facts.Conversation.Pending)
			require.Equal(t, uint8(1), facts.Conversation.Type)
			if record.Identity.UID == "migrationalice" {
				pendingRead = facts.Conversation.ReadSeq
			}
		}
		return nil
	}))
	require.ElementsMatch(t, []string{"migrationalice", "migrationbob"}, subscribers)
	require.ElementsMatch(t, []uint64{2096462572973723648, 2096462572977917952, 2096462573007278080, 2096462782110109696}, messages)
	require.Equal(t, uint64(3), bobRead)
	require.Equal(t, uint64(3), bobDeleted)
	require.Equal(t, uint64(1), pendingRead)
	resumed, err := migration.SelectSources(ctx, capture, catalog, spool, reader, nil)
	require.NoError(t, err)
	require.Equal(t, selected, resumed)
}
