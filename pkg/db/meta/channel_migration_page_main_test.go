package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelMigrationPageResumesAfterCompletedTask(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(8)
	ctx := context.Background()
	a := testChannelMigrationTask("a", "channel-a")
	b := testChannelMigrationTask("b", "channel-b")
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, a))
	require.NoError(t, shard.CreateChannelMigrationTask(ctx, b))
	tasks, cursor, done, err := shard.ListActiveChannelMigrationTaskPage(ctx, ChannelMigrationTaskCursor{}, 1)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []ChannelMigrationTask{a}, tasks)
	require.NoError(t, shard.AdvanceChannelMigrationTask(ctx, ChannelMigrationTaskAdvance{
		Guard: channelMigrationTaskGuard(a), Status: ChannelMigrationStatusCompleted, Phase: ChannelMigrationPhaseClearFence,
		UpdatedAtMS: a.UpdatedAtMS + 1, CompletedAtMS: a.UpdatedAtMS + 2,
	}))
	tasks, _, done, err = shard.ListActiveChannelMigrationTaskPage(ctx, cursor, 1)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []ChannelMigrationTask{b}, tasks)
}
