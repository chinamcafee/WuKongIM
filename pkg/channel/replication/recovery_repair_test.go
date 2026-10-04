package replication

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func TestRepairQuorumPrefixAppendsFromProvedDonorWithoutReplacingLocalRows(t *testing.T) {
	key := ch.ChannelKey("1:repair-owner")
	id := ch.ChannelID{ID: "repair-owner", Type: 1}
	first, firstTail := recoveryMutationAfter(t, key, id, 1, 0, ch.EntryIdentity{})
	first.Committed = 1
	donorSecond, donorSecondTail := recoveryMutationAfter(t, key, id, 2, 1, firstTail)
	donorThird, donorThirdTail := recoveryMutationAfter(t, key, id, 3, 2, donorSecondTail)

	store, err := NewStoreAdapter(StoreAdapterConfig{
		Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	if results := store.Sync(context.Background(), []Mutation{first, donorSecond}); len(results) != 2 ||
		!results[0].Outcome.Durable() || !results[1].Outcome.Durable() {
		t.Fatalf("Sync(local prefix) = %+v", results)
	}
	donorState := ReplicaState{
		LEO: 3, Committed: 1, Manifest: donorThird.Manifest, TailIdentity: donorThirdTail,
	}
	repairedState := donorState
	repairedState.Committed = 3
	dispatcher := &scriptedRecoveryFetchDispatcher{result: FetchResult{
		State: donorState,
		Proposals: []RecoveryProposal{
			{Manifest: donorThird.Manifest, Records: donorThird.Records},
		},
	}}

	got, err := repairQuorumPrefix(context.Background(), recoveryRepairRequest{
		ChannelKey: key, ChannelID: id, Leader: 1, Local: 1,
		Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute, MaxPageBytes: 32 << 10,
		Selection: recoverySelection{
			Index: 3, Identity: donorThirdTail, CertifiedCommitted: 1, CertifiedIdentity: firstTail,
			Supporters: []recoverySupporter{{Voter: 2, State: donorState}, {Voter: 3, State: donorState}},
		},
	}, dispatcher, store)
	if err != nil {
		t.Fatalf("repairQuorumPrefix() error = %v", err)
	}
	if got != repairedState {
		t.Fatalf("repaired state = %+v, want quorum-proven prefix committed as %+v", got, repairedState)
	}
	if len(dispatcher.queries) != 1 || dispatcher.queries[0].Donor != 2 || dispatcher.queries[0].From != 3 ||
		dispatcher.queries[0].Through != 3 || dispatcher.queries[0].Previous != donorSecondTail {
		t.Fatalf("fetch queries = %+v, want one exact donor-2 suffix query", dispatcher.queries)
	}
	loaded, loadErr := store.Load(context.Background(), LoadBatch{Items: []LoadRequest{{
		ChannelKey: key, ChannelID: id, ProbeIndexes: []uint64{1, 2, 3},
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State != repairedState {
		t.Fatalf("Load(repaired) = %+v, error %v", loaded, loadErr)
	}
	wantEntries := []EntryProbe{
		{Index: 1, Present: true, Identity: firstTail},
		{Index: 2, Present: true, Identity: donorSecondTail},
		{Index: 3, Present: true, Identity: donorThirdTail},
	}
	if !reflect.DeepEqual(loaded.Items[0].Entries, wantEntries) {
		t.Fatalf("repaired identities = %+v, want %+v", loaded.Items[0].Entries, wantEntries)
	}
}

func TestRepairQuorumPrefixPreservesUnprovenLocalSuffix(t *testing.T) {
	for _, scenario := range []string{"divergent-tail", "advanced-after-proof", "checkpoint-only"} {
		t.Run(scenario, func(t *testing.T) {
			h, _, a := convergenceFixture(t)
			first, firstTail := recoveryMutationAfter(t, a.Key, a.ChannelID, 1, 0, ch.EntryIdentity{})
			second, secondTail := recoveryMutationAfter(t, a.Key, a.ChannelID, 2, 1, firstTail)
			conflict, _ := recoveryMutationAfter(t, a.Key, a.ChannelID, 9, 1, firstTail)
			writeRecoveryFixture(t, h, 1, first)
			selected := ReplicaState{LEO: 2, Manifest: second.Manifest, TailIdentity: secondTail}
			expectedErr := ch.ErrLogConflict
			switch scenario {
			case "divergent-tail":
				writeRecoveryFixture(t, h, 1, conflict)
			case "advanced-after-proof":
				writeRecoveryFixture(t, h, 1, second)
				selected = ReplicaState{LEO: 1, Manifest: first.Manifest, TailIdentity: firstTail}
				expectedErr = errRecoveryProbeIncomplete
			case "checkpoint-only":
				writeRecoveryFixture(t, h, 1, second)
				expectedErr = nil
			}
			before, err := loadRecoveryReplicaState(context.Background(), h.stores[1], a.Key, a.ChannelID, []uint64{1, 2})
			if err != nil {
				t.Fatal(err)
			}
			dispatcher := &scriptedRecoveryFetchDispatcher{}
			_, err = repairQuorumPrefix(context.Background(), recoveryRepairRequest{
				ChannelKey: a.Key, ChannelID: a.ChannelID, Leader: 1, Local: 1,
				Voters: a.Voters, Quorum: 2, Timeout: time.Minute, MaxPageBytes: 64 << 10,
				Selection: recoverySelection{Index: selected.LEO, Identity: selected.TailIdentity,
					Supporters: []recoverySupporter{{Voter: 2, State: selected}, {Voter: 3, State: selected}}},
			}, dispatcher, h.stores[1])
			if !errors.Is(err, expectedErr) {
				t.Fatalf("repair error = %v, want %v", err, expectedErr)
			}
			after, err := loadRecoveryReplicaState(context.Background(), h.stores[1], a.Key, a.ChannelID, []uint64{1, 2})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "checkpoint-only" {
				before.State.Committed = 2
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("local rows changed: before=%+v after=%+v", before, after)
			}
			if len(dispatcher.queries) != 0 {
				t.Fatal("unexpected suffix fetch")
			}
		})
	}
}

func TestRepairQuorumPrefixStreamsArbitraryDistanceInBoundedAtomicPages(t *testing.T) {
	const last = uint64(257)
	key := ch.ChannelKey("1:repair-paged")
	id := ch.ChannelID{ID: "repair-paged", Type: 1}
	proposals := make([]RecoveryProposal, 0, last)
	previous := ch.EntryIdentity{}
	for index := uint64(1); index <= last; index++ {
		mutation, tail := recoveryMutationAfter(t, key, id, index, index-1, previous)
		proposals = append(proposals, RecoveryProposal{Manifest: mutation.Manifest, Records: mutation.Records})
		previous = tail
	}
	donorState := ReplicaState{LEO: last, Manifest: proposals[len(proposals)-1].Manifest, TailIdentity: previous}
	store, err := NewStoreAdapter(StoreAdapterConfig{
		Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 1, MaxBatchBytes: 4 << 20,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	dispatcher := &pagedRecoveryFetchDispatcher{state: donorState, proposals: proposals}
	got, err := repairQuorumPrefix(context.Background(), recoveryRepairRequest{
		ChannelKey: key, ChannelID: id, Leader: 1, Local: 1,
		Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute, MaxPageBytes: 1 << 20,
		Selection: recoverySelection{
			Index: last, Identity: previous,
			Supporters: []recoverySupporter{{Voter: 2, State: donorState}, {Voter: 3, State: donorState}},
		},
	}, dispatcher, store)
	if err != nil {
		t.Fatalf("repairQuorumPrefix() error = %v", err)
	}
	if got.LEO != last || got.Committed != last || got.TailIdentity != previous {
		t.Fatalf("repaired state = %+v, want committed tail %d", got, last)
	}
	if len(dispatcher.queries) != 2 || dispatcher.queries[0].From != 1 || dispatcher.queries[0].Through != 256 ||
		dispatcher.queries[1].From != 257 || dispatcher.queries[1].Through != 257 {
		t.Fatalf("fetch queries = %+v, want bounded pages [1,256] and [257,257]", dispatcher.queries)
	}
}

type scriptedRecoveryFetchDispatcher struct {
	result  FetchResult
	queries []recoveryFetchQuery
}

type pagedRecoveryFetchDispatcher struct {
	state     ReplicaState
	proposals []RecoveryProposal
	queries   []recoveryFetchQuery
}

func (d *pagedRecoveryFetchDispatcher) submitRecoveryFetch(_ context.Context, query recoveryFetchQuery, complete func(FetchResult, error)) error {
	d.queries = append(d.queries, query)
	page := make([]RecoveryProposal, 0, query.Through-query.From+1)
	for _, proposal := range d.proposals {
		if proposal.Manifest.BaseOffset+1 >= query.From && proposal.Manifest.LastOffset <= query.Through {
			page = append(page, proposal)
		}
	}
	request := FetchRequest{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
		Leader: query.Leader, Follower: query.Donor, Expected: query.Expected,
		From: query.From, Through: query.Through, Previous: query.Previous, MaxBytes: query.MaxBytes,
	}
	complete(FetchResult{Proof: fetchProofFor(request), State: d.state, Proposals: page}, nil)
	return nil
}

func (d *scriptedRecoveryFetchDispatcher) submitRecoveryFetch(_ context.Context, query recoveryFetchQuery, complete func(FetchResult, error)) error {
	d.queries = append(d.queries, query)
	result := d.result
	result.Proof = fetchProofFor(FetchRequest{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
		Leader: query.Leader, Follower: query.Donor, Expected: query.Expected,
		From: query.From, Through: query.Through, Previous: query.Previous, MaxBytes: query.MaxBytes,
	})
	complete(result, nil)
	return nil
}

func recoveryMutationAfter(t *testing.T, key ch.ChannelKey, id ch.ChannelID, marker uint64, base uint64, previous ch.EntryIdentity) (Mutation, ch.EntryIdentity) {
	t.Helper()
	commandID := ch.CommandID{}
	binary.BigEndian.PutUint64(commandID[24:], marker)
	record := ch.Record{
		ID: marker, Epoch: 3, FromUID: "sender", ClientMsgNo: "m-" + strconv.FormatUint(marker, 10),
		Payload: []byte{byte(marker)}, SizeBytes: 1, ServerTimestampMS: int64(marker),
	}
	manifest, entries, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: commandID, BaseOffset: base, LastOffset: base + 1,
		PreviousTerm: previous.LeaderTerm, PreviousIndex: base, PreviousDigest: previous.Digest,
	}, []ch.Record{record})
	if !ok {
		t.Fatalf("SealProposalManifest(marker=%d) failed", marker)
	}
	return Mutation{ChannelKey: key, ChannelID: id, Manifest: manifest, Records: []ch.Record{record}}, entries[0]
}

func TestRepairQuorumPrefixWaitsForSuffixProofAndResumesWithoutLosingDurableTail(t *testing.T) {
	key := ch.ChannelKey("1:recovery-rejoin")
	id := ch.ChannelID{ID: "recovery-rejoin", Type: 1}
	first, firstTail := recoveryMutationAfter(t, key, id, 1, 0, ch.EntryIdentity{})
	first.Committed = 1
	second, secondTail := recoveryMutationAfter(t, key, id, 2, 1, firstTail)
	adapter, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 4, MaxBatchBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range adapter.Sync(context.Background(), []Mutation{first, second}) {
		if !result.Outcome.Durable() || result.Err != nil {
			t.Fatalf("seed=%+v", result)
		}
	}
	local := ReplicaState{LEO: 2, Committed: 1, Manifest: second.Manifest, TailIdentity: secondTail}
	behind := ReplicaState{LEO: 1, Committed: 1, Manifest: first.Manifest, TailIdentity: firstTail}
	request := recoveryRepairRequest{ChannelKey: key, ChannelID: id, Leader: 1, Local: 1, Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Second, MaxPageBytes: 32 << 10,
		Selection: recoverySelection{Index: 1, Identity: firstTail, CertifiedCommitted: 1, CertifiedIdentity: firstTail, Supporters: []recoverySupporter{{Voter: 1, State: local}, {Voter: 2, State: behind}}}}
	dispatcher := &scriptedRecoveryFetchDispatcher{}
	if _, err := repairQuorumPrefix(context.Background(), request, dispatcher, adapter); !errors.Is(err, ch.ErrNotReady) {
		t.Fatalf("incomplete suffix proof error=%v, want retryable not ready", err)
	}
	loaded, err := loadRecoveryReplicaState(context.Background(), adapter, key, id, nil)
	if err != nil || loaded.State != local || len(dispatcher.queries) != 0 {
		t.Fatalf("uncertain durable tail changed: %+v err=%v fetches=%d", loaded, err, len(dispatcher.queries))
	}
	// The second durable supporter returns after the next fault/rejoin. Its
	// complete prefix proof permits normal repair without replaying the SEND.
	request.Selection.Index = 2
	request.Selection.Identity = secondTail
	request.Selection.Supporters = []recoverySupporter{{Voter: 1, State: local}, {Voter: 3, State: local}}
	dispatcher.result = FetchResult{State: local, Proposals: []RecoveryProposal{{Manifest: second.Manifest, Records: second.Records}}}
	got, err := repairQuorumPrefix(context.Background(), request, dispatcher, adapter)
	if err != nil || got.LEO != 2 || got.Committed != 2 || got.TailIdentity != secondTail {
		t.Fatalf("resumed recovery=%+v error=%v", got, err)
	}
}
