package replication

import (
	"context"
	"fmt"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

func TestInstallRestoresAcknowledgedSuffixWithOneDurableSupporterUnavailable(t *testing.T) {
	for _, leader := range []ch.NodeID{1, 3} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("leader=%d/empty=%t", leader, empty), func(t *testing.T) {
				key := ch.ChannelKey("1:successive-faults")
				id := ch.ChannelID{ID: "successive-faults", Type: 1}
				first, firstTail := recoveryMutationAfter(t, key, id, 1, 0, ch.EntryIdentity{})
				first.Committed = 1
				second, secondTail := recoveryMutationAfter(t, key, id, 2, 1, firstTail)
				rows := map[ch.NodeID][]Mutation{1: {first}, 2: {first, second}, 3: {first, second}}
				if empty {
					rows[1] = nil
				}
				runtimes, stores, _ := newRecoveryConvergenceCluster(t, rows, []ch.NodeID{1, 3})
				authority := Authority{Key: key, ChannelID: id, ID: AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 8}, Leader: leader, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
				installed, err := runtimes[leader].Log().Install(context.Background(), authority)
				require.NoError(t, err)
				require.Equal(t, uint64(3), installed.HW, "two original entries plus the current authority barrier")
				for _, node := range []ch.NodeID{1, 3} {
					got, err := loadRecoveryReplicaState(context.Background(), stores[node], key, id, []uint64{2})
					require.NoError(t, err)
					require.Equal(t, secondTail, got.Entries[0].Identity, "preserve the acknowledged message identity")
				}
			})
		}
	}
}

func TestRecoveryConvergenceRejectsDivergentSurvivorWithoutChangingAnyLog(t *testing.T) {
	key := ch.ChannelKey("1:divergent-recovery")
	id := ch.ChannelID{ID: "divergent-recovery", Type: 1}
	first, tail := recoveryMutationAfter(t, key, id, 1, 0, ch.EntryIdentity{})
	first.Committed = 1
	a, _ := recoveryMutationAfter(t, key, id, 2, 1, tail)
	b, _ := recoveryMutationAfter(t, key, id, 3, 1, tail)
	// Synchronous probes ensure every conflicting survivor is actually observed;
	// a fast quorum is not required to wait for an unobserved third voter.
	h, log, _ := convergenceFixture(t)
	h.unavailable = 0
	for voter, rows := range map[ch.NodeID][]Mutation{1: {first}, 2: {first, a}, 3: {first, b}} {
		for _, row := range rows {
			writeRecoveryFixture(t, h, voter, row)
		}
	}
	stores := h.stores
	before := make(map[ch.NodeID]ReplicaState)
	for node, store := range stores {
		got, err := loadRecoveryReplicaState(context.Background(), store, key, id, nil)
		require.NoError(t, err)
		before[node] = got.State
	}
	authority := Authority{Key: key, ChannelID: id, ID: AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 8}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
	_, err := log.Install(context.Background(), authority)
	require.ErrorIs(t, err, ch.ErrNotReady)
	for node, store := range stores {
		got, err := loadRecoveryReplicaState(context.Background(), store, key, id, nil)
		require.NoError(t, err)
		require.Equal(t, before[node], got.State)
	}
}

func newRecoveryConvergenceCluster(t *testing.T, rows map[ch.NodeID][]Mutation, reachable []ch.NodeID) (map[ch.NodeID]*Runtime, map[ch.NodeID]ReplicaStore, *runtimeTestRouter) {
	t.Helper()
	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer)}
	runtimes := make(map[ch.NodeID]*Runtime)
	stores := make(map[ch.NodeID]ReplicaStore)
	for node, mutations := range rows {
		store, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 64, MaxBatchBytes: 4 << 20})
		require.NoError(t, err)
		for len(mutations) > 0 {
			n := min(len(mutations), 64)
			for _, got := range store.Sync(context.Background(), mutations[:n]) {
				require.NoError(t, got.Err)
				require.True(t, got.Outcome.Durable())
			}
			mutations = mutations[n:]
		}
		runtime, err := NewRuntime(RuntimeConfig{LocalNode: node, Store: store, Link: runtimeTestLink{from: node, router: router}, Goroutines: goruntimeregistry.New()})
		require.NoError(t, err)
		runtimes[node] = runtime
		stores[node] = store
	}
	for _, node := range reachable {
		router.register(node, runtimes[node].ExchangeServer())
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for _, runtime := range runtimes {
			require.NoError(t, runtime.Close(ctx))
		}
	})
	return runtimes, stores, router
}

// A page boundary yields without admission; a later Install resumes using the
// fresh durable frontier, without replaying a SEND or retaining the whole tail.
func TestRecoveryConvergenceResumesAcrossBoundedPages(t *testing.T) {
	key := ch.ChannelKey("1:paged-convergence")
	id := ch.ChannelID{ID: "paged-convergence", Type: 1}
	var rows []Mutation
	var previous ch.EntryIdentity
	for i := uint64(1); i <= 257; i++ {
		item, tail := recoveryMutationAfter(t, key, id, i, i-1, previous)
		rows = append(rows, item)
		previous = tail
	}
	runtimes, stores, _ := newRecoveryConvergenceCluster(t, map[ch.NodeID][]Mutation{1: nil, 2: rows, 3: rows}, []ch.NodeID{1, 3})
	authority := Authority{Key: key, ChannelID: id, ID: AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 8}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
	_, err := runtimes[1].Log().Install(context.Background(), authority)
	require.ErrorIs(t, err, ch.ErrNotReady)
	partial, err := loadRecoveryReplicaState(context.Background(), stores[1], key, id, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(256), partial.State.LEO)
	installed, err := runtimes[1].Log().Install(context.Background(), authority)
	require.NoError(t, err)
	require.Equal(t, uint64(258), installed.HW)
	got, err := loadRecoveryReplicaState(context.Background(), stores[1], key, id, []uint64{257})
	require.NoError(t, err)
	require.Equal(t, previous, got.Entries[0].Identity)
}
