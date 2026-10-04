package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestMigrationMetaApplyYieldsOnContentionAndDoesNotCacheFailedAuthority(t *testing.T) {
	id := ch.ChannelID{ID: "bounded-apply", Type: 2}
	meta := ch.Meta{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	runtime := &repairActivationRuntime{}
	svc, err := NewService(Config{LocalNode: 2, Runtime: runtime})
	require.NoError(t, err)
	lock := &svc.metaApplyLocks[channelMetaApplyLockIndex(id)]
	lock.Lock()
	err = svc.ApplyMetaContext(context.Background(), meta)
	lock.Unlock()
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.Zero(t, runtime.activations)
	runtime.activationErr = context.DeadlineExceeded
	require.ErrorIs(t, svc.ApplyMetaContext(context.Background(), meta), context.DeadlineExceeded)
	_, ok := svc.metaCache.get(id)
	require.False(t, ok)
	runtime.activationErr = nil
	require.NoError(t, svc.ApplyMetaContext(context.Background(), meta))
	_, ok = svc.metaCache.get(id)
	require.True(t, ok)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, svc.ApplyMetaContext(canceled, meta), context.Canceled)
	require.Equal(t, 2, runtime.activations)
}
