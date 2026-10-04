package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/stretchr/testify/require"
)

type repairActivationRuntime struct {
	benchRuntimeFake
	activations   int
	activationErr error
}

func (r *repairActivationRuntime) ApplyMetaContext(ctx context.Context, _ ch.Meta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.activations++
	return r.activationErr
}

func TestRepairActivationRejectsForeignReplicaAndInactiveMetadata(t *testing.T) {
	id := ch.ChannelID{ID: "repair", Type: 2}
	for _, tc := range []struct {
		name     string
		replicas []ch.NodeID
		status   ch.Status
	}{
		{"foreign", []ch.NodeID{1, 3}, ch.StatusActive},
		{"inactive", []ch.NodeID{1, 2}, ch.StatusCreating},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := &repairActivationRuntime{}
			svc, err := NewService(Config{LocalNode: 2, Runtime: runtime, MetaSource: NewStaticMetaSource([]ch.Meta{{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: tc.replicas, Status: tc.status}})})
			require.NoError(t, err)
			_, err = svc.ActivateReplicaForRepair(context.Background(), id)
			require.ErrorIs(t, err, ch.ErrStaleMeta)
			require.Zero(t, runtime.activations)
		})
	}
}

func TestRepairActivationDoesNotInventProofOnFailureOrMetadataRace(t *testing.T) {
	id := ch.ChannelID{ID: "repair", Type: 2}
	runtime := &repairActivationRuntime{}
	svc, err := NewService(Config{LocalNode: 2, Runtime: runtime, MetaSource: NewStaticMetaSource([]ch.Meta{{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, Status: ch.StatusActive}})})
	require.NoError(t, err)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.ActivateReplicaForRepair(canceled, id)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, runtime.activations)
	runtime.activationErr = ch.ErrNotReady
	_, err = svc.ActivateReplicaForRepair(context.Background(), id)
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.Zero(t, runtime.probeCalls)
	runtime.activationErr = nil
	_, err = svc.ActivateReplicaForRepair(context.Background(), id)
	require.ErrorIs(t, err, ch.ErrChannelNotFound)
	runtime.probe = ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: 1, LeaderEpoch: 2, Status: ch.StatusActive}}}
	_, err = svc.ActivateReplicaForRepair(context.Background(), id)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
}

// repairProbeStore injects exact durability and authority changes at the read seam.
type repairProbeStore struct {
	replication.ReplicaStore
	state  replication.ReplicaState
	err    error
	onLoad func()
}

func (s *repairProbeStore) Load(context.Context, replication.LoadBatch) (replication.LoadBatchResult, error) {
	if s.onLoad != nil {
		s.onLoad()
	}
	return replication.LoadBatchResult{Items: []replication.LoadResult{{State: s.state}}}, s.err
}

func TestRepairActivationFencesDurableProgress(t *testing.T) {
	for _, mode := range []string{"fresh", "older_history", "durable_tail", "runtime_role", "runtime_missing", "runtime_epoch", "runtime_fence", "metadata_epoch", "durable_epoch", "durable_term", "durable_fence", "invalid_hw", "load_failed"} {
		t.Run(mode, func(t *testing.T) {
			id := ch.ChannelID{ID: "repair-fence", Type: 2}
			meta := ch.Meta{ID: id, Epoch: 4, LeaderEpoch: 5, RouteGeneration: 6, Leader: 1, Replicas: []ch.NodeID{1, 2}, Status: ch.StatusActive}
			source := NewStaticMetaSource([]ch.Meta{meta})
			runtime := &repairActivationRuntime{}
			runtime.probe = ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: id, ChannelEpoch: 4, LeaderEpoch: 5, Role: ch.RoleFollower, Status: ch.StatusActive}}}
			store := &repairProbeStore{state: replication.ReplicaState{LEO: 20, Committed: 19, Manifest: ch.ProposalManifest{ChannelEpoch: 4, LeaderTerm: 5, FenceVersion: 6}}}
			switch mode {
			case "older_history":
				store.state.Manifest.ChannelEpoch--
				store.state.Manifest.LeaderTerm = 99
			case "durable_tail":
				store.state.TailIdentity = ch.EntryIdentity{ChannelEpoch: 5, LeaderTerm: 1, FenceVersion: 1}
			case "runtime_role":
				store.onLoad = func() { runtime.probe.Channels[0].Role = ch.RoleLeader }
			case "runtime_missing":
				store.onLoad = func() { runtime.probe.Channels = nil }
			case "runtime_epoch":
				store.onLoad = func() { runtime.probe.Channels[0].ChannelEpoch++ }
			case "runtime_fence":
				store.onLoad = func() { runtime.probe.Channels[0].WriteFence.Version++ }
			case "metadata_epoch":
				store.onLoad = func() { next := source.items[id]; next.Epoch++; source.items[id] = next }
			case "durable_epoch":
				store.state.Manifest.ChannelEpoch++
			case "durable_term":
				store.state.Manifest.LeaderTerm++
			case "durable_fence":
				store.state.Manifest.FenceVersion++
			case "invalid_hw":
				store.state.Committed = 21
			case "load_failed":
				store.err = context.DeadlineExceeded
			}
			svc, err := NewService(Config{LocalNode: 2, Runtime: runtime, MetaSource: source})
			require.NoError(t, err)
			svc.replicaStore = store
			got, err := svc.ActivateReplicaForRepair(context.Background(), id)
			if mode == "fresh" || mode == "older_history" {
				require.NoError(t, err)
				require.EqualValues(t, 20, got.LEO)
				require.EqualValues(t, 19, got.HW)
			} else if mode == "load_failed" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.ErrorIs(t, err, ch.ErrStaleMeta)
			}
		})
	}
}
