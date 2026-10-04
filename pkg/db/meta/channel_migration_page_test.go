package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActiveMigrationPageResumesAfterDeletedCursorAndSeparatesChannelTypes(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(8)
	ctx := context.Background()
	a := testChannelMigrationTask("first", "same")
	b := testChannelMigrationTask("second", "same")
	b.ChannelType = a.ChannelType + 1
	c := testChannelMigrationTask("third", "tail")
	for _, task := range []ChannelMigrationTask{c, b, a} {
		require.NoError(t, shard.CreateChannelMigrationTask(ctx, task))
	}
	page, cursor, done, err := shard.ListActiveChannelMigrationTaskPage(ctx, ChannelMigrationTaskCursor{}, 1)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []ChannelMigrationTask{a}, page)
	require.NoError(t, shard.AdvanceChannelMigrationTask(ctx, ChannelMigrationTaskAdvance{Guard: channelMigrationTaskGuard(a), Status: ChannelMigrationStatusCompleted, Phase: ChannelMigrationPhaseClearFence, UpdatedAtMS: a.UpdatedAtMS + 1, CompletedAtMS: a.UpdatedAtMS + 2}))
	page, cursor, done, err = shard.ListActiveChannelMigrationTaskPage(ctx, cursor, 1)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []ChannelMigrationTask{b}, page)
	page, cursor, done, err = shard.ListActiveChannelMigrationTaskPage(ctx, cursor, 1)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []ChannelMigrationTask{c}, page)
	page, _, done, err = shard.ListActiveChannelMigrationTaskPage(ctx, cursor, 1)
	require.NoError(t, err)
	require.True(t, done)
	require.Empty(t, page)
	_, _, _, err = shard.ListActiveChannelMigrationTaskPage(ctx, ChannelMigrationTaskCursor{ChannelType: 1}, 1)
	require.Error(t, err)
}
