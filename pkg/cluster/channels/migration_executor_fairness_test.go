package channels

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// A retrying Channel must not prevent an independent failover from advancing.
func TestMigrationExecutorAdvancesOtherTasksWhileFirstRecoveryWaits(t *testing.T) {
	now := time.Unix(100, 0)
	tasks := make(migrationFairSource, 2)
	for i, name := range []string{"waiting", "ready"} {
		tasks[i] = testLeaderFailoverExecutorTask(ch.ChannelID{ID: name, Type: 2})
		tasks[i].TaskID = "shared-per-channel-id"
		tasks[i].OwnerNodeID = 2
		tasks[i].OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	}
	meta := &migrationFairMeta{}
	store := &migrationFairStore{}
	e := NewMigrationExecutor(MigrationExecutorConfig{LocalNode: 2, Source: &migrationRotatingSource{tasks: tasks}, Store: store,
		Runtime: &fakeMigrationExecutorRuntime{}, Meta: meta, Clock: func() time.Time { return now }, TaskLimit: 2})
	require.ErrorIs(t, e.RunOnce(context.Background()), ch.ErrNotReady)
	require.Equal(t, []string{"waiting", "ready"}, meta.attempted)
	require.Equal(t, []string{"ready"}, store.advanced)
}

type migrationFairSource []metadb.ChannelMigrationTask

type migrationRotatingSource struct {
	tasks migrationFairSource
	next  int
}

func (s *migrationRotatingSource) ListRunnableMigrationTasks(_ context.Context, _ uint64, limit int) ([]metadb.ChannelMigrationTask, error) {
	out := make([]metadb.ChannelMigrationTask, 0, limit)
	for i := 0; i < limit && i < len(s.tasks); i++ {
		out = append(out, s.tasks[s.next])
		s.next = (s.next + 1) % len(s.tasks)
	}
	return out, nil
}

type migrationFairMeta struct{ attempted []string }

func (m *migrationFairMeta) GetChannelRuntimeMeta(_ context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	m.attempted = append(m.attempted, id)
	if id == "waiting" {
		return metadb.ChannelRuntimeMeta{}, ch.ErrNotReady
	}
	return metadb.ChannelRuntimeMeta{ChannelID: id, ChannelType: typ, Leader: 1, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2}, nil
}

type migrationFairStore struct {
	MigrationTaskStore
	advanced []string
}

func (s *migrationFairStore) Advance(_ context.Context, task metadb.ChannelMigrationTask, _ int64, phase metadb.ChannelMigrationPhase, status metadb.ChannelMigrationStatus, _ string) error {
	if phase == metadb.ChannelMigrationPhaseProbeTarget && status == metadb.ChannelMigrationStatusRunning {
		s.advanced = append(s.advanced, task.ChannelID)
	}
	return nil
}

// Cancellation models a consumed tick without waiting for real wall-clock time.
func TestMigrationExecutorTimeoutDoesNotSkipNextTask(t *testing.T) {
	now := time.Unix(100, 0)
	tasks := make(migrationFairSource, 2)
	for i, name := range []string{"waiting", "ready"} {
		tasks[i] = testLeaderFailoverExecutorTask(ch.ChannelID{ID: name, Type: 2})
		tasks[i].TaskID = "shared-per-channel-id"
		tasks[i].OwnerNodeID = 2
		tasks[i].OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	meta := &migrationCancelMeta{cancel: cancel}
	store := &migrationFairStore{}
	e := NewMigrationExecutor(MigrationExecutorConfig{LocalNode: 2, Source: &migrationRotatingSource{tasks: tasks}, Store: store,
		Runtime: &fakeMigrationExecutorRuntime{}, Meta: meta, Clock: func() time.Time { return now }, TaskLimit: 2})
	require.ErrorIs(t, e.RunOnce(ctx), context.Canceled)
	require.Empty(t, store.advanced)
	_ = e.RunOnce(context.Background())
	require.Equal(t, []string{"ready"}, store.advanced)
}

type migrationCancelMeta struct{ cancel context.CancelFunc }

func (m *migrationCancelMeta) GetChannelRuntimeMeta(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	if id == "waiting" {
		m.cancel()
		return metadb.ChannelRuntimeMeta{}, context.Canceled
	}
	return (&migrationFairMeta{}).GetChannelRuntimeMeta(ctx, id, typ)
}
