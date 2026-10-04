//go:build integration

package meta

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/stretchr/testify/require"
)

// A rejected command in another Slot must not masquerade as failure to restore
// this Slot's valid durable watermark merely because their fsync was grouped.
func TestMetaBatchRejectedNeighborDoesNotFailRestoreWatermark(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	store.db.committer.Close()
	store.db.committer = commit.NewCoordinator(store.engine, commit.Config{FlushWindow: 100 * time.Millisecond, QueueSize: 8, MaxRequests: 2})
	valid := store.db.NewBatch()
	defer valid.Close()
	require.NoError(t, valid.UpsertUser(1, User{UID: "restored", Token: "native"}))
	require.NoError(t, valid.SetSlotAppliedIndex(1, 17))
	invalid := store.db.NewBatch()
	defer invalid.Close()
	require.NoError(t, invalid.UpsertUser(2, User{UID: "must-not-exist", Token: "native"}))
	invalid.addOp(2, func(context.Context, *batchCommitState, *engine.Batch) error { return dberrors.ErrNotFound })
	start := make(chan struct{})
	results := make([]chan error, 2)
	for i, batch := range []*Batch{valid, invalid} {
		results[i] = make(chan error, 1)
		go func(result chan error) { <-start; result <- batch.Commit(context.Background()) }(results[i])
	}
	close(start)
	require.NoError(t, <-results[0], "another Slot's expected rejection must not poison the valid restore")
	require.ErrorIs(t, <-results[1], dberrors.ErrNotFound)
	index, err := store.db.SlotAppliedIndex(context.Background(), 1)
	require.NoError(t, err)
	require.EqualValues(t, 17, index)
	_, found, err := store.db.HashSlot(1).GetUser(context.Background(), "restored")
	require.NoError(t, err)
	require.True(t, found)
	_, found, err = store.db.HashSlot(2).GetUser(context.Background(), "must-not-exist")
	require.NoError(t, err)
	require.False(t, found, "partial writes of the rejected batch must remain absent")
}
