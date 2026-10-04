package replication

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// A write acknowledged by nodes 1 and 3 is still committed when node 3 is
// unavailable and node 2 has not received the trailing copy. A recovery read
// quorum intersects that write quorum at node 1; it cannot prove absence.
func TestQuorumInstallPreservesAcknowledgedTailWithUnavailableVoter(t *testing.T) {
	for _, name := range []string{"empty-prefix", "nonempty-prefix", "remote-donor"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			harness := &unavailableRecoveryHarness{replicaHarness: newReplicaHarness(t, 1, 2, 3), unavailable: 3}
			key := ch.ChannelKey("1:ack-before-pause")
			id := ch.ChannelID{ID: "ack-before-pause", Type: 1}
			base := uint64(0)
			previous := ch.EntryIdentity{}
			if name == "nonempty-prefix" {
				prefix, tail := recoveryMutationAfter(t, key, id, 40, 0, previous)
				prefix.Committed = 1
				for _, voter := range []ch.NodeID{1, 2, 3} {
					result := harness.stores[voter].Sync(ctx, []Mutation{prefix})
					if len(result) != 1 || !result[0].Outcome.Durable() || result[0].Err != nil {
						t.Fatalf("prefix voter %d: %+v", voter, result)
					}
				}
				base, previous = 1, tail
			}
			mutation, _ := recoveryMutationAfter(t, key, id, 41, base, previous)
			donor := ch.NodeID(1)
			if name == "remote-donor" {
				donor = 2
			}
			for _, voter := range []ch.NodeID{donor, 3} {
				result := harness.stores[voter].Sync(ctx, []Mutation{mutation})
				if len(result) != 1 || !result[0].Outcome.Durable() || result[0].Err != nil {
					t.Fatalf("write voter %d: %+v", voter, result)
				}
			}
			log, err := newQuorumLog(quorumLogConfig{
				Local: 1, Store: harness.stores[1], Recovery: harness, Durability: harness,
				RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10,
				MaxChannels: 8, MaxVoters: 3, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16,
			})
			if err != nil {
				t.Fatal(err)
			}
			authority := Authority{Key: key, ChannelID: id, ID: AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 8}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
			_, err = log.Install(ctx, authority)
			// Inspect the original durable proposal even when Install incorrectly succeeds,
			// so the regression reports destructive recovery instead of only an error code.
			loaded, loadErr := harness.stores[1].Load(ctx, LoadBatch{Items: []LoadRequest{{ChannelKey: key, ChannelID: id}}})
			if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State.LEO < base+1 {
				t.Fatalf("acknowledged durable tail changed after incomplete recovery: %+v, %v (install error=%v)", loaded, loadErr, err)
			}
			if err != nil {
				t.Fatalf("Install with one unavailable voter = %v; want a new quorum over the preserved prefix", err)
			}
			// The unavailable node must not be needed for either recovery or subsequent writes.
			next := Proposal{Key: key, Expected: authority.ID, CommandID: ch.CommandID{31: 99}, Records: []ch.Record{{ID: 99, Epoch: authority.ID.ChannelEpoch, FromUID: "sender", ClientMsgNo: "after-failover", Payload: []byte("next"), SizeBytes: 4, ServerTimestampMS: 99}}}
			receipt, commitErr := log.Commit(ctx, next)
			if commitErr != nil || receipt.Last <= base+1 {
				t.Fatalf("Commit while voter unavailable = %+v, %v", receipt, commitErr)
			}
			harness.unavailable = 0
			installed, err := log.Install(ctx, authority)
			if err != nil || installed.HW < base+1 {
				t.Fatalf("Install after voter recovery = %+v, %v", installed, err)
			}
			proposal := harness.stores[1].(commandStore).LookupCommands(ctx, []CommandLookup{{ChannelKey: key, ChannelID: id, CommandID: mutation.Manifest.CommandID, MaxRecords: 256, MaxBytes: 64 << 10}})
			if len(proposal) != 1 || proposal[0].Err != nil || !proposal[0].Found || proposal[0].Manifest != mutation.Manifest {
				t.Fatalf("original proposal missing after recovery: %+v", proposal)
			}
		})
	}
}

type unavailableRecoveryHarness struct {
	*replicaHarness
	unavailable   ch.NodeID
	beforeReplica func(ch.NodeID, durableProposal)
	beforeFetch   func(recoveryFetchQuery) error
}

func (h *unavailableRecoveryHarness) submitRecoveryProbe(ctx context.Context, query recoveryProbeQuery, complete func(ProbeResult, error)) error {
	if query.Voter == h.unavailable {
		complete(ProbeResult{}, errors.New("voter unavailable"))
		return nil
	}
	return h.replicaHarness.submitRecoveryProbe(ctx, query, complete)
}

func (h *unavailableRecoveryHarness) submitLocal(_ context.Context, proposal durableProposal, complete func(durabilityCompletion)) error {
	return h.submitReplica(context.Background(), proposal.leader, proposal, complete)
}
func (h *unavailableRecoveryHarness) submitReplica(_ context.Context, voter ch.NodeID, proposal durableProposal, complete func(durabilityCompletion)) error {
	if voter == h.unavailable {
		complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: errors.New("voter unavailable")})
		return nil
	}
	if h.beforeReplica != nil {
		h.beforeReplica(voter, proposal)
	}
	h.replicaHarness.submit(voter, proposal, complete)
	return nil
}

func (h *unavailableRecoveryHarness) submitRecoveryFetch(ctx context.Context, query recoveryFetchQuery, complete func(FetchResult, error)) error {
	if query.Donor == h.unavailable {
		complete(FetchResult{}, errors.New("voter unavailable"))
		return nil
	}
	if h.beforeFetch != nil {
		if err := h.beforeFetch(query); err != nil {
			complete(FetchResult{}, err)
			return nil
		}
	}
	return h.replicaHarness.submitRecoveryFetch(ctx, query, complete)
}
