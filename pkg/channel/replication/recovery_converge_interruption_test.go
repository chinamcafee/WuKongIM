package replication

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func convergenceFixture(t *testing.T) (*unavailableRecoveryHarness, *quorumLog, Authority) {
	t.Helper()
	h := &unavailableRecoveryHarness{replicaHarness: newReplicaHarness(t, 1, 2, 3), unavailable: 3}
	a := Authority{Key: "1:converge", ChannelID: ch.ChannelID{ID: "converge", Type: 1}, ID: AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 8}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
	log, err := newQuorumLog(quorumLogConfig{Local: 1, Store: h.stores[1], Recovery: h, Durability: h, RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10, MaxChannels: 8, MaxVoters: 3, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16})
	require.NoError(t, err)
	return h, log, a
}

func writeRecoveryFixture(t *testing.T, h *unavailableRecoveryHarness, voter ch.NodeID, mutation Mutation) {
	t.Helper()
	results := h.stores[voter].Sync(context.Background(), []Mutation{mutation})
	require.Len(t, results, 1)
	require.NoError(t, results[0].Err)
	require.True(t, results[0].Outcome.Durable())
}

func TestRecoveryConvergenceRejectsDivergentSurvivorWithoutWrites(t *testing.T) {
	h, log, a := convergenceFixture(t)
	first, _ := recoveryMutationAfter(t, a.Key, a.ChannelID, 41, 0, ch.EntryIdentity{})
	conflict, _ := recoveryMutationAfter(t, a.Key, a.ChannelID, 42, 0, ch.EntryIdentity{})
	writeRecoveryFixture(t, h, 1, first)
	writeRecoveryFixture(t, h, 2, conflict)
	_, err := log.Install(context.Background(), a)
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.Zero(t, h.syncCalls, "conflicting tails must not be rewritten")
	for voter, want := range map[ch.NodeID]ch.ProposalManifest{1: first.Manifest, 2: conflict.Manifest} {
		loaded, err := h.stores[voter].Load(context.Background(), LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
		require.NoError(t, err)
		require.NoError(t, loaded.Items[0].Err)
		require.Equal(t, want, loaded.Items[0].State.Manifest)
	}
}

func TestRecoveryConvergencePreservesOldWriteAcknowledgedDuringCopy(t *testing.T) {
	h, log, a := convergenceFixture(t)
	first, tail := recoveryMutationAfter(t, a.Key, a.ChannelID, 41, 0, ch.EntryIdentity{})
	late, lateTail := recoveryMutationAfter(t, a.Key, a.ChannelID, 42, 1, tail)
	writeRecoveryFixture(t, h, 1, first)
	writeRecoveryFixture(t, h, 3, first)
	// The new owner cannot probe node 3, but an old in-flight write reaches node 1
	// while copying the older prefix to node 2. It must not be truncated as stale.
	h.beforeReplica = func(voter ch.NodeID, p durableProposal) {
		if voter != 2 || p.manifest != first.Manifest {
			return
		}
		h.beforeReplica = nil
		writeRecoveryFixture(t, h, 1, late)
		writeRecoveryFixture(t, h, 3, late)
	}
	_, err := log.Install(context.Background(), a)
	require.ErrorIs(t, err, ch.ErrNotReady)
	installed, err := log.Install(context.Background(), a)
	require.NoError(t, err)
	require.Greater(t, installed.HW, uint64(2))
	for _, voter := range []ch.NodeID{1, 2} {
		found := h.stores[voter].(commandStore).LookupCommands(context.Background(), []CommandLookup{{ChannelKey: a.Key, ChannelID: a.ChannelID, CommandID: late.Manifest.CommandID, MaxRecords: 256, MaxBytes: 64 << 10}})
		require.Len(t, found, 1)
		require.NoError(t, found[0].Err)
		require.True(t, found[0].Found)
		require.Equal(t, late.Manifest, found[0].Manifest)
	}
	// A competing late old-leader proposal cannot replace the installed barrier.
	oldNext, _ := recoveryMutationAfter(t, a.Key, a.ChannelID, 43, 2, lateTail)
	for _, voter := range []ch.NodeID{1, 2} {
		results := h.stores[voter].Sync(context.Background(), []Mutation{oldNext})
		require.Len(t, results, 1)
		require.False(t, results[0].Outcome.Durable())
		require.Error(t, results[0].Err)
	}
}

func TestRecoveryConvergenceResumesDurablePagesAfterInterruption(t *testing.T) {
	h, log, a := convergenceFixture(t)
	previous := ch.EntryIdentity{}
	for i := uint64(1); i <= 600; i++ {
		m, tail := recoveryMutationAfter(t, a.Key, a.ChannelID, i, i-1, previous)
		writeRecoveryFixture(t, h, 1, m)
		writeRecoveryFixture(t, h, 3, m)
		previous = tail
	}
	interrupted := errors.New("interrupted donor read")
	h.beforeFetch = func(q recoveryFetchQuery) error {
		require.LessOrEqual(t, q.Through-q.From+1, uint64(256))
		if q.From > 1 {
			return interrupted
		}
		return nil
	}
	_, err := log.Install(context.Background(), a)
	require.ErrorIs(t, err, ch.ErrNotReady, "one durable page yields before the next donor read")
	_, err = log.Install(context.Background(), a)
	require.ErrorIs(t, err, interrupted)
	loaded, err := h.stores[2].Load(context.Background(), LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
	require.NoError(t, err)
	require.NoError(t, loaded.Items[0].Err)
	progress := loaded.Items[0].State.LEO
	require.Greater(t, progress, uint64(0))
	require.Less(t, progress, uint64(600))
	firstFetch := uint64(0)
	h.beforeFetch = func(q recoveryFetchQuery) error {
		if firstFetch == 0 {
			firstFetch = q.From
		}
		return nil
	}
	installed, err := log.Install(context.Background(), a)
	for attempt := 0; err != nil && attempt < 4; attempt++ {
		installed, err = log.Install(context.Background(), a)
		if err == nil {
			break
		}
		require.ErrorIs(t, err, ch.ErrNotReady)
	}
	require.NoError(t, err)
	require.GreaterOrEqual(t, installed.HW, uint64(600))
	require.Equal(t, progress+1, firstFetch, "resume must use the target's durable prefix")
}

func TestRecoveryConvergenceRejectsStaleAuthorityBeforeCopy(t *testing.T) {
	h, log, a := convergenceFixture(t)
	m, _ := recoveryMutationAfter(t, a.Key, a.ChannelID, 41, 0, ch.EntryIdentity{})
	writeRecoveryFixture(t, h, 1, m)
	a.ID.LeaderTerm = 4
	_, err := log.Install(context.Background(), a)
	require.ErrorIs(t, err, ch.ErrStaleMeta)
	require.Zero(t, h.syncCalls)
}
