package replication

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// A resumed former leader may retain its sequencer after its durable replica
// has accepted the new authority. That evidence must invalidate stale routing.
func TestResumedQuorumOwnerRejectsAuthorityBehindDurableReplica(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "pending"}[pending], func(t *testing.T) {
			ctx := context.Background()
			h, old, a := convergenceFixture(t)
			h.unavailable = 0
			_, err := old.Install(ctx, a)
			require.NoError(t, err)
			proposal := func(marker byte, authority Authority) Proposal {
				return Proposal{Key: a.Key, Expected: authority.ID, CommandID: ch.CommandID{31: marker}, Records: []ch.Record{{ID: uint64(marker), Epoch: a.ID.ChannelEpoch, FromUID: "sender", ClientMsgNo: string([]byte{marker}), Payload: []byte{marker}, SizeBytes: 1, ServerTimestampMS: int64(marker)}}}
			}
			first, err := old.Commit(ctx, proposal(41, a))
			require.NoError(t, err)
			staleProposal := proposal(43, a)
			if pending {
				h.loseResponses = 3
				_, err = old.Commit(ctx, staleProposal)
				require.Error(t, err)
				require.NotNil(t, old.existingChannel(a.Key).pending)
			}
			nextAuthority := a
			nextAuthority.Leader = 2
			nextAuthority.ID.LeaderTerm++
			nextAuthority.ID.FenceVersion++
			cfg := old.cfg
			cfg.Local = 2
			cfg.Store = h.stores[2]
			next, err := newQuorumLog(cfg)
			require.NoError(t, err)
			_, err = next.Install(ctx, nextAuthority)
			require.NoError(t, err)
			latest, err := next.Commit(ctx, proposal(42, nextAuthority))
			require.NoError(t, err)
			require.Greater(t, latest.Last, first.Last)
			loaded, err := h.stores[1].Load(ctx, LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
			require.NoError(t, err)
			require.NoError(t, loaded.Items[0].Err)
			require.Equal(t, nextAuthority.ID.LeaderTerm, loaded.Items[0].State.Manifest.LeaderTerm)
			_, err = old.Commit(ctx, staleProposal)
			if pending {
				require.NoError(t, err, "an exact already-durable old proposal may return its proven receipt")
				_, err = old.Commit(ctx, proposal(44, a))
			}
			require.ErrorIs(t, err, ch.ErrStaleMeta, "newer durable authority is stale routing evidence, not a business command conflict")
			after, err := h.stores[1].Load(ctx, LoadBatch{Items: []LoadRequest{{ChannelKey: a.Key, ChannelID: a.ChannelID}}})
			require.NoError(t, err)
			require.Equal(t, loaded, after, "stale owner must not alter or acknowledge the new authority log")
			require.False(t, old.existingChannel(a.Key).ready)
			require.Nil(t, old.existingChannel(a.Key).pending)
		})
	}
}

func TestDurableAuthorityAdvanceRequiresValidNewerFrontier(t *testing.T) {
	h, log, a := convergenceFixture(t)
	_, err := log.Install(context.Background(), a)
	require.NoError(t, err)
	state := log.existingChannel(a.Key)
	newer := a
	newer.ID.LeaderTerm++
	m, _ := recoveryMutationAfter(t, a.Key, a.ChannelID, 41, 0, ch.EntryIdentity{})
	m.Manifest.ChannelEpoch = newer.ID.ChannelEpoch
	m.Manifest.LeaderTerm = newer.ID.LeaderTerm
	m.Manifest.FenceVersion = newer.ID.FenceVersion
	sealed, entries, ok := ch.SealProposalManifest(m.Manifest, m.Records)
	require.True(t, ok)
	valid := ReplicaState{LEO: sealed.LastOffset, Manifest: sealed, TailIdentity: entries[len(entries)-1]}
	for _, tc := range []struct {
		name   string
		result LoadBatchResult
		err    error
		want   bool
	}{
		{name: "newer", result: LoadBatchResult{Items: []LoadResult{{State: valid}}}, want: true},
		{name: "absent"},
		{name: "read error", err: errors.New("read failed")},
		{name: "item error", result: LoadBatchResult{Items: []LoadResult{{State: valid, Err: errors.New("corrupt")}}}},
		{name: "malformed", result: LoadBatchResult{Items: []LoadResult{{State: ReplicaState{LEO: valid.LEO, Manifest: valid.Manifest}}}}},
		{name: "empty", result: LoadBatchResult{Items: []LoadResult{{}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log.cfg.Store = &frontierEvidenceStore{ReplicaStore: h.stores[1], result: tc.result, err: tc.err}
			require.Equal(t, tc.want, log.durableAuthorityAdvanced(context.Background(), state))
		})
	}
	same := valid
	same.Manifest.LeaderTerm = a.ID.LeaderTerm
	same.TailIdentity.LeaderTerm = a.ID.LeaderTerm
	log.cfg.Store = &frontierEvidenceStore{ReplicaStore: h.stores[1], result: LoadBatchResult{Items: []LoadResult{{State: same}}}}
	require.False(t, log.durableAuthorityAdvanced(context.Background(), state))
}

type frontierEvidenceStore struct {
	ReplicaStore
	result LoadBatchResult
	err    error
}

func (s *frontierEvidenceStore) Load(context.Context, LoadBatch) (LoadBatchResult, error) {
	return s.result, s.err
}
