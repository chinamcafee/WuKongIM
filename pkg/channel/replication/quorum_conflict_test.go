package replication

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestQuorumRetryAfterReceiptEvictionDoesNotWaitForUnavailableVoter(t *testing.T) {
	h, log, a := convergenceFixture(t)
	log.cfg.MaxRetainedCommands = 1
	_, err := log.Install(context.Background(), a)
	require.NoError(t, err)
	proposal := func(marker byte) Proposal {
		return Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{31: marker}, Records: []ch.Record{{ID: uint64(marker), Epoch: a.ID.ChannelEpoch, FromUID: "sender", ClientMsgNo: string([]byte{marker}), Payload: []byte{marker}, SizeBytes: 1, ServerTimestampMS: int64(marker)}}}
	}
	first := proposal(41)
	original, err := log.Commit(context.Background(), first)
	require.NoError(t, err)
	second, err := log.Commit(context.Background(), proposal(42))
	require.NoError(t, err)
	require.Greater(t, second.Last, original.Last)
	require.NotContains(t, log.existingChannel(a.Key).retained, first.CommandID)
	retried, err := log.Commit(context.Background(), first)
	require.NoError(t, err, "a proven local command conflict must reach durable lookup despite an unknown follower")
	require.Equal(t, original, retried)
	next, err := log.Commit(context.Background(), proposal(43))
	require.NoError(t, err, "a duplicate must not leave an impossible pending proposal")
	require.Equal(t, second.Last+1, next.Last)
	require.Equal(t, ch.NodeID(3), h.unavailable)
}

func TestDurableLocalConflictDoesNotWaitForPausedPeer(t *testing.T) {
	d := &localConflictDispatcher{}
	result, err := runDurableRound(context.Background(), 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{first: 1, last: 1}, d)
	require.ErrorIs(t, err, ch.ErrLogConflict)
	require.Equal(t, ch.AppendOutcomeConflict, result.outcome)
	require.False(t, result.localDurable)
	require.Len(t, d.accepted, 1, "local conflict must not admit additional followers")
	// The accepted peer still owns its callback, even after the round returned.
	d.accepted[0](durabilityCompletion{outcome: ch.AppendOutcomeDurable})
}

type localConflictDispatcher struct{ accepted []func(durabilityCompletion) }

func (*localConflictDispatcher) submitLocal(_ context.Context, _ durableProposal, complete func(durabilityCompletion)) error {
	complete(durabilityCompletion{outcome: ch.AppendOutcomeConflict, err: ch.ErrLogConflict})
	return nil
}
func (d *localConflictDispatcher) submitReplica(_ context.Context, _ ch.NodeID, _ durableProposal, complete func(durabilityCompletion)) error {
	d.accepted = append(d.accepted, complete)
	return nil
}
