package replication

import (
	"context"
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestLearnerAuthorityRejectsVotingOverlapAndNonVoterLeader(t *testing.T) {
	a := Authority{Key: "2:learner", ChannelID: ch.ChannelID{ID: "learner", Type: 2}, ID: AuthorityID{1, 1, 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Learners: []ch.NodeID{4}, WriteQuorum: 2}
	require.True(t, validAuthority(a))
	for _, learners := range [][]ch.NodeID{{0}, {2}, {4, 4}} {
		invalid := a
		invalid.Learners = learners
		require.False(t, validAuthority(invalid))
	}
	a.Leader = 4
	require.False(t, validAuthority(a), "a learner cannot be the authority leader")
}

func TestLearnerRepairResumesPagesAndCancelsOnMembershipChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := &runtimeRepairOwner{ctx: ctx, maxPending: 4, pending: make(map[runtimeRepairKey]*runtimeRepairEntry), notify: make(chan struct{}, 1)}
	a := Authority{Key: "2:learner", ChannelID: ch.ChannelID{ID: "learner", Type: 2}, ID: AuthorityID{1, 1, 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Learners: []ch.NodeID{4}, WriteQuorum: 2}
	o.InstallAuthority(a)
	repair := testFollowerRepair(a, 4)
	repair.manifest.LastOffset, repair.committed = 1000, 1000
	o.RecordFollowerRepair(repair)
	key, _, entry, version, ok := o.take()
	require.True(t, ok)
	o.retainProgress(key, entry, version, 257)
	o.finish(key, entry, version, false)
	_, resumed, _, _, ok := o.take()
	require.True(t, ok)
	require.EqualValues(t, 257, resumed.needFrom, "retry must not restart a large history from the beginning")
	a.ID.ChannelEpoch++
	a.Learners = []ch.NodeID{5}
	o.InstallAuthority(a)
	require.ErrorIs(t, entry.ctx.Err(), context.Canceled)
	o.RecordFollowerRepair(repair)
	require.Zero(t, o.pendingCount(), "old authority cannot reinstate the removed learner")
}

func TestLearnerRepairRetainsProgressDuringConcurrentCommits(t *testing.T) {
	for _, newGap := range []bool{false, true} {
		t.Run(fmt.Sprint("new_gap=", newGap), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o := &runtimeRepairOwner{ctx: ctx, maxPending: 4, pending: make(map[runtimeRepairKey]*runtimeRepairEntry), notify: make(chan struct{}, 1)}
			a := Authority{Key: "2:learner", ChannelID: ch.ChannelID{ID: "learner", Type: 2}, ID: AuthorityID{1, 1, 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Learners: []ch.NodeID{4}, WriteQuorum: 2}
			o.InstallAuthority(a)
			repair := testFollowerRepair(a, 4)
			repair.manifest.LastOffset, repair.committed = 1000, 1000
			o.RecordFollowerRepair(repair)
			key, _, entry, version, ok := o.take()
			require.True(t, ok)
			later := repair
			later.needFrom, later.manifest.LastOffset, later.committed = 1001, 1001, 1001
			o.RecordFollowerRepair(later)
			if newGap {
				o.RecordFollowerRepair(repair)
			}
			o.retainProgress(key, entry, version, 257)
			o.finish(key, entry, version, false)
			_, resumed, _, _, ok := o.take()
			require.True(t, ok)
			if newGap {
				require.EqualValues(t, 1, resumed.needFrom)
			} else {
				require.EqualValues(t, 257, resumed.needFrom, "tail growth must not discard a completed page")
			}
			require.EqualValues(t, 1001, resumed.manifest.LastOffset)
		})
	}
}
