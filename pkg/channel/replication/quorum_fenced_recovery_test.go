package replication

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// Native leader transfer verifies the new authority before clearing its write
// fence. Recovery must complete while business Commit remains fenced.
func TestFencedAuthorityRecoversWithoutAdmittingBusinessWrites(t *testing.T) {
	h := newReplicaHarness(t, 1, 2, 3)
	log, err := newQuorumLog(quorumLogConfig{Local: 1, Store: h.stores[1], Recovery: h, Durability: h, RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10, MaxChannels: 8, MaxVoters: 3, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16})
	require.NoError(t, err)
	ctx := context.Background()
	authority := Authority{Key: "2:native-fence", ChannelID: ch.ChannelID{ID: "native-fence", Type: 2}, ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
	_, err = log.Install(ctx, authority)
	require.NoError(t, err)
	proposal := Proposal{Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{31: 1}, Records: []ch.Record{{ID: 101, Epoch: 1, FromUID: "sender", ClientMsgNo: "native", Payload: []byte("native"), SizeBytes: 6, ServerTimestampMS: 111}}}
	_, err = log.Commit(ctx, proposal)
	require.NoError(t, err)
	fenced := authority
	fenced.ID.LeaderTerm++
	fenced.ID.FenceVersion++
	fenced.WriteFence = ch.WriteFence{Token: "native-transfer", Version: 2, Reason: ch.WriteFenceReasonLeaderTransfer}
	installed, err := log.Install(ctx, fenced)
	require.NoError(t, err, "new leader verification must recover under the active task fence")
	require.EqualValues(t, 2, installed.HW, "original message plus native recovery barrier")
	before := h.syncCalls
	again, err := log.Install(ctx, fenced)
	require.NoError(t, err)
	require.Equal(t, installed, again)
	proposal.Expected = fenced.ID
	proposal.CommandID = ch.CommandID{31: 2}
	proposal.Records[0].ID = 102
	proposal.Records[0].ClientMsgNo = "native-next"
	_, err = log.Commit(ctx, proposal)
	require.ErrorIs(t, err, ch.ErrWriteFenced)
	require.Equal(t, before, h.syncCalls, "fenced business append and repeated install must not write")
	require.True(t, log.Release(authority.Key, fenced.ID))
	_, err = log.Install(ctx, authority)
	require.ErrorIs(t, err, ch.ErrStaleMeta, "cold recovery must reject authority behind the durable tail")
	again, err = log.Install(ctx, fenced)
	require.NoError(t, err)
	require.Equal(t, installed, again)
	require.Equal(t, before, h.syncCalls, "cold fenced recovery reuses the existing durable barrier")
	unfenced := fenced
	unfenced.ID.FenceVersion++
	unfenced.WriteFence = ch.WriteFence{}
	_, err = log.Install(ctx, unfenced)
	require.NoError(t, err)
	proposal.Expected = unfenced.ID
	receipt, err := log.Commit(ctx, proposal)
	require.NoError(t, err)
	require.Greater(t, receipt.HW, installed.HW)
}
