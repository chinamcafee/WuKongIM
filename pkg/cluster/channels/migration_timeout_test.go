package channels

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// A silent failover target must not inherit the supervisor's unbounded lifetime.
func TestFailoverExecutorBoundsTargetProbeWithoutAdvancingProof(t *testing.T) {
	now := time.Now()
	id := ch.ChannelID{ID: "timeout-target", Type: 2}
	task := testLeaderFailoverExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseDrainLeader
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	task.FenceToken, task.FenceVersion = task.TaskID, 4
	task.FenceUntilMS = now.Add(time.Minute).UnixMilli()
	meta := testMigrationRuntimeMeta(id)
	meta.Leader, meta.LeaderEpoch = 1, 20
	meta.WriteFenceToken, meta.WriteFenceVersion = task.TaskID, 4
	meta.WriteFenceReason = uint8(ch.WriteFenceReasonFailover)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &deadlineMigrationRuntime{t: t}
	executor := NewMigrationExecutor(MigrationExecutorConfig{
		LocalNode: 2, FailoverPhaseLimit: 8, Source: fakeMigrationExecutorSource{store: store}, Store: store,
		Runtime: runtime, Meta: fakeMigrationExecutorMetaReader{meta: &meta}, Clock: func() time.Time { return now },
	})
	require.ErrorIs(t, executor.RunOnce(context.Background()), context.DeadlineExceeded)
	require.True(t, runtime.called)
	require.Equal(t, task, store.task)
	require.Empty(t, store.ops)
}

type deadlineMigrationRuntime struct {
	fakeMigrationExecutorRuntime
	t      *testing.T
	called bool
}

func (r *deadlineMigrationRuntime) ProbeChannel(ctx context.Context, _ uint64, _ string, _ uint8) (ch.RuntimeProbeChannel, error) {
	r.called = true
	deadline, ok := ctx.Deadline()
	require.True(r.t, ok, "target probe inherited unbounded migration-loop context")
	require.LessOrEqual(r.t, time.Until(deadline), 5*time.Second)
	return ch.RuntimeProbeChannel{}, context.DeadlineExceeded
}
