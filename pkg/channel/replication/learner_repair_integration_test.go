//go:build integration

package replication

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestLearnerCatchUpPreservesPagedHistoryAndCarriesQuorumAfterPromotion(t *testing.T) {
	key := ch.ChannelKey("1:learner-repair")
	id := ch.ChannelID{ID: "learner-repair", Type: 1}
	var rows []Mutation
	var tail ch.EntryIdentity
	for i := uint64(1); i <= 257; i++ {
		item, next := recoveryMutationAfter(t, key, id, i, i-1, tail)
		item.Committed = i
		rows = append(rows, item)
		tail = next
	}
	runtimes, stores, router := newRecoveryConvergenceCluster(t, map[ch.NodeID][]Mutation{1: rows, 2: rows, 3: rows, 4: nil}, []ch.NodeID{1, 3, 4})
	authority := Authority{Key: key, ChannelID: id, ID: AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 8}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Learners: []ch.NodeID{4}, WriteQuorum: 2,
		WriteFence: ch.WriteFence{Token: "replacement", Version: 1, Reason: ch.WriteFenceReasonReplicaReplace}}
	installed, err := runtimes[1].Log().Install(context.Background(), authority)
	require.NoError(t, err)
	require.Equal(t, uint64(258), installed.HW)
	require.Eventually(t, func() bool {
		got, err := loadRecoveryReplicaState(context.Background(), stores[4], key, id, []uint64{257})
		return err == nil && got.State.LEO == installed.LEO && got.State.Committed == installed.HW && len(got.Entries) == 1 && got.Entries[0].Identity == tail
	}, 3*time.Second, time.Millisecond, "learner must receive the exact historical chain and committed barrier without another SEND")

	// Once promoted, the spare must carry the quorum with both old followers unavailable.
	router.mu.Lock()
	delete(router.servers, 3)
	router.mu.Unlock()
	promoted := authority
	promoted.ID.ChannelEpoch++
	promoted.ID.FenceVersion++
	promoted.Voters = []ch.NodeID{1, 3, 4}
	promoted.Learners = nil
	promoted.WriteFence = ch.WriteFence{}
	_, err = runtimes[1].Log().Install(context.Background(), promoted)
	require.NoError(t, err)
	receipt, err := runtimes[1].Log().Commit(context.Background(), Proposal{Key: key, Expected: promoted.ID, CommandID: ch.CommandID{99}, Records: []ch.Record{{ID: 999, Epoch: promoted.ID.ChannelEpoch, Payload: []byte("after-promotion"), SizeBytes: len("after-promotion"), ServerTimestampMS: 999}}})
	require.NoError(t, err)
	require.Greater(t, receipt.HW, installed.HW)
}

func TestLearnerCannotSupplyMissingVoterQuorum(t *testing.T) {
	key := ch.ChannelKey("1:learner-not-voter")
	id := ch.ChannelID{ID: "learner-not-voter", Type: 1}
	runtimes, _, _ := newRecoveryConvergenceCluster(t, map[ch.NodeID][]Mutation{1: nil, 2: nil, 3: nil, 4: nil}, []ch.NodeID{1, 4})
	authority := Authority{Key: key, ChannelID: id, ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Learners: []ch.NodeID{4}, WriteQuorum: 2}
	_, err := runtimes[1].Log().Install(context.Background(), authority)
	require.Error(t, err, "leader plus learner cannot establish a voter quorum")
}
