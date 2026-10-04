package channels

import (
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type preemptibleRepairStore struct {
	fakeRepairScannerStore
	aborts int
}

func (s *preemptibleRepairStore) AbortReplicaReplacementForFailover(context.Context, ch.ChannelID, uint64, uint64) (bool, error) {
	s.aborts++
	return true, nil
}

func TestRepairScannerPreemptsReplacementBeforeDeadLeaderFailover(t *testing.T) {
	id := ch.ChannelID{ID: "repair-preemption", Type: 1}
	source := newFakeRepairScannerSource(id)
	source.active[id] = true
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(id), failoverPlannerMeta(id)}}
	store := &preemptibleRepairStore{}
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 10, MaxPagesPerTick: 10, MaxTasksPerTick: 1}, source, store)
	_, err := scanner.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, store.aborts, "dead leader must not remain hidden behind a replacement task; mutation budget still applies")
	require.Empty(t, store.requests, "failover must use a fresh metadata snapshot on the next tick")
}

func TestReplicaReplacementPreemptionPreservesPromotedAndChangedAuthority(t *testing.T) {
	for _, name := range []string{"pre-promotion", "promoted", "verified", "embedded", "leader-changed", "epoch-changed", "terminal", "missing"} {
		t.Run(name, func(t *testing.T) {
			id := ch.ChannelID{ID: "preempt-guard", Type: 1}
			meta := testMigrationRuntimeMeta(id)
			task := testMigrationTask(id, "guarded-replacement")
			task.Status = metadb.ChannelMigrationStatusRunning
			task.Phase = metadb.ChannelMigrationPhaseCutoverFence
			meta.Replicas = append(meta.Replicas, task.TargetNode)
			leader, epoch := meta.Leader, meta.LeaderEpoch
			switch name {
			case "promoted":
				meta.ISR = append(meta.ISR, task.TargetNode)
			case "verified":
				task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
			case "embedded":
				task.EmbeddedLeaderTransfer = true
			case "leader-changed":
				leader++
			case "epoch-changed":
				epoch++
			case "terminal":
				task.Status = metadb.ChannelMigrationStatusCompleted
			}
			active := map[uint16]metadb.ChannelMigrationTask{22: task}
			if name == "missing" {
				delete(active, 22)
			}
			reader := &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{22: meta}, active: active}
			proposer := &fakeMigrationProposer{}
			now := time.UnixMilli(task.UpdatedAtMS + 1)
			store := NewMigrationStore(MigrationStoreConfig{LocalNode: 2, Router: fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 22, SlotID: 32, Leader: 2}}}, Reader: reader, Proposer: proposer, Now: func() time.Time { return now }})
			aborted, err := store.AbortReplicaReplacementForFailover(context.Background(), id, leader, epoch)
			require.NoError(t, err)
			require.Equal(t, name == "pre-promotion", aborted)
			if !aborted {
				require.Empty(t, proposer.lastCommand)
				return
			}
			expected := metadb.ChannelMigrationAbortRequest{Guard: migrationTaskGuard(task, task.UpdatedAtMS), RuntimeGuard: migrationRuntimeGuard(meta), Status: metadb.ChannelMigrationStatusAborted, Phase: task.Phase, UpdatedAtMS: now.UnixMilli(), CompletedAtMS: now.UnixMilli(), LastError: "leader unavailable before replica promotion"}
			require.Equal(t, metafsm.EncodeAbortChannelMigrationCommand(expected), proposer.lastCommand)
		})
	}
}
