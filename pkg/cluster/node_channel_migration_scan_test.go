package cluster

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMigrationTaskScanRotatesShardsAndContinuesPastWaitingTasks(t *testing.T) {
	var scan migrationTaskScan
	read := func(_ context.Context, slot uint16, after metadb.ChannelMigrationTaskCursor, limit int) ([]metadb.ChannelMigrationTask, metadb.ChannelMigrationTaskCursor, bool, error) {
		require.Equal(t, 1, limit)
		id := "first"
		if after.ChannelID != "" {
			id = "second"
		}
		return []metadb.ChannelMigrationTask{{ChannelID: id, TargetNode: uint64(slot)}}, metadb.ChannelMigrationTaskCursor{ChannelID: id}, id == "second", nil
	}
	for i, want := range []string{"first", "first", "second", "second", "first"} {
		tasks, err := scan.page(context.Background(), []uint16{1, 5}, 1, read)
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		require.Equal(t, want, tasks[0].ChannelID)
		require.Equal(t, []uint64{1, 5}[i%2], tasks[0].TargetNode)
	}
	_, err := scan.page(context.Background(), []uint16{5}, 1, read)
	require.NoError(t, err)
	require.NotContains(t, scan.cursors, uint16(1), "lost authority discards its cursor")
}
