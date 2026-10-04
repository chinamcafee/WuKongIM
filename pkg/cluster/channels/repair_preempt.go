package channels

import (
	"context"
	"errors"
	"slices"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// AbortReplicaReplacementForFailover releases only an unpromoted replacement
// when the scanner has observed its leader unavailable. Existing Slot commands
// atomically fence the task version and metadata, remove the unpromoted learner,
// and clear only the task-owned write fence. Election uses the next fresh scan.
func (s *MigrationStore) AbortReplicaReplacementForFailover(ctx context.Context, id ch.ChannelID, leader, leaderEpoch uint64) (bool, error) {
	route, meta, err := s.readRuntimeMeta(ctx, id)
	if err != nil {
		return false, err
	}
	if leader == 0 || meta.Leader != leader || meta.LeaderEpoch != leaderEpoch {
		return false, nil
	}
	task, found, err := s.reader.GetActiveChannelMigrationTask(ctx, route.HashSlot, id.ID, int64(id.Type))
	if err != nil || !found {
		return false, err
	}
	if task.IsTerminal() || task.Kind != metadb.ChannelMigrationKindReplicaReplace || task.EmbeddedLeaderTransfer || slices.Contains(meta.ISR, task.TargetNode) {
		return false, nil
	}
	switch task.Phase {
	case metadb.ChannelMigrationPhaseValidate, metadb.ChannelMigrationPhaseAddLearner,
		metadb.ChannelMigrationPhaseBootstrapTarget, metadb.ChannelMigrationPhaseWarmCatchUp,
		metadb.ChannelMigrationPhaseCutoverFence, metadb.ChannelMigrationPhaseFinalTargetCatchUp,
		metadb.ChannelMigrationPhasePromoteAndRemove:
	default:
		return false, nil
	}
	err = s.abortAtRuntimeMeta(ctx, route, meta, task, "leader unavailable before replica promotion")
	if errors.Is(err, metadb.ErrStaleMeta) {
		return false, nil
	}
	return err == nil, err
}
