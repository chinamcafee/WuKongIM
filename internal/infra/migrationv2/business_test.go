package migrationv2_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestOriginalBusinessFactsPreserveCredentialsAndImportTheRecordedEventProjection(t *testing.T) {
	ctx := context.Background()
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	reader := migrationv2.Reader{}
	var state meta.MessageEventState
	var cursor meta.MessageEventCursor
	deviceCount := 0
	userCount, channelCount := 0, 0
	_, err := reader.ReadStoppedNode(ctx, migration.NodeOptions{NodeID: 1, Options: migration.Options{DataDir: dir, ShardCount: 2}}, func(row migration.Row) error {
		if row.Kind != migration.Primary || (row.Table != "Device" && row.Table != "MessageEventState" && row.Table != "User" && row.Table != "ChannelInfo") {
			return nil
		}
		id, err := reader.Identify(row)
		if err != nil {
			return err
		}
		facts, err := reader.DecodeBusiness(row, id)
		if err != nil {
			return err
		}
		if facts.User != nil {
			userCount++
			require.NotEmpty(t, facts.User.UID)
			require.NotZero(t, facts.User.SourceID)
		}
		if facts.Channel != nil {
			channelCount++
			require.Equal(t, "migrationgroup", facts.Channel.ID)
			// Original AddSubscribers does not update the old ChannelInfo counter.
			// Its public GetSubscriberCount scans the actual member table.
			require.Zero(t, facts.Channel.SubscriberCount)
			require.False(t, facts.Channel.Ban)
		}
		if facts.Device != nil {
			deviceCount++
			require.Equal(t, "synthetic-"+facts.Device.UID, facts.Device.Token)
			require.Equal(t, uint64(1), facts.Device.Flag)
			require.Equal(t, uint8(1), facts.Device.Level)
			require.NotZero(t, facts.Device.SourceID)
		}
		if facts.EventState != nil {
			state = *facts.EventState
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.Equal(t, 2, userCount)
	require.Equal(t, 1, channelCount)
	require.Equal(t, 2, deviceCount)
	require.Equal(t, "migrationgroup", state.ChannelID)
	require.Equal(t, "original-v2-0", state.ClientMsgNo)
	require.Equal(t, uint64(2), state.LastMsgEventSeq)
	require.Equal(t, "event-close", state.LastEventID)
	require.Equal(t, "closed", state.Status)
	require.JSONEq(t, `{"kind":"text","text":"持久快照"}`, string(state.SnapshotPayload))
	cursor = meta.MessageEventCursor{ChannelID: state.ChannelID, ChannelType: state.ChannelType, ClientMsgNo: state.ClientMsgNo, LastMsgEventSeq: 2}
	db, err := meta.Open(filepath.Join(t.TempDir(), "target-meta"))
	require.NoError(t, err)
	defer db.Close()
	shard := db.MetaDB().HashSlot(3)
	require.NoError(t, shard.ImportMessageEventProjection(ctx, state, cursor))
	got, exists, err := shard.GetMessageEventState(ctx, "migrationgroup", 2, "original-v2-0", "main")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, state, got)
}
