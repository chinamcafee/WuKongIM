package migrationv3_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestImportedMessageBatchesRemainRecoverable(t *testing.T) {
	for _, test := range []struct {
		name, channelID     string
		count, payloadBytes int
	}{
		{name: "large batch", channelID: "budget-room", count: 64, payloadBytes: 32 << 10},
		// Native bytes fit one MiB but the neutral identity/protocol cost does not.
		{name: "replication overhead", channelID: "budget-room", count: 64, payloadBytes: 16300},
		// The native row repeats Channel ID; peer record accounting does not.
		{name: "native encoding overhead", channelID: strings.Repeat("room", 100), count: 64, payloadBytes: 16200},
		// One message has 102 bytes of neutral overhead: exact budget must work.
		{name: "exact single record boundary", channelID: "budget-room", count: 1, payloadBytes: (1 << 20) - 102},
	} {
		t.Run(test.name, func(t *testing.T) { assertImportedMessagesRecover(t, test.channelID, test.count, test.payloadBytes) })
	}
}

func assertImportedMessagesRecover(t *testing.T, channelID string, count, payloadBytes int) {
	t.Helper()
	ctx := context.Background()
	w, plan, report, id := messageImportPlan(t, channelID, count, payloadBytes)
	require.NoError(t, migrationv3.Install(ctx, plan, report, w))
	verify, err := (migrationv3.Inspector{}).Open(ctx, plan, plan.Nodes[0])
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, verify.Close()) })
	leo, hw, err := verify.Progress(ctx, migration.ChannelIdentity{ID: id.ID, Type: id.Type})
	require.NoError(t, err)
	require.Equal(t, uint64(count), leo)
	require.Equal(t, leo, hw)
	require.NoError(t, verify.Close())

	openReplica := func(path string) (replication.ReplicaStore, *store.MessageDBFactory) {
		factory := store.NewMessageDBFactory(path)
		t.Cleanup(func() { require.NoError(t, factory.Close()) })
		adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: replication.MaxExchangeBatchItems, MaxBatchBytes: replication.MaxExchangeBatchBytes})
		require.NoError(t, err)
		return adapter, factory
	}
	donor, _ := openReplica(filepath.Join(plan.Nodes[0].DataDir, "messages"))
	target, targetFactory := openReplica(filepath.Join(t.TempDir(), "empty-replica"))
	key := ch.ChannelKeyForID(id)
	loaded, err := donor.Load(ctx, replication.LoadBatch{Items: []replication.LoadRequest{{ChannelKey: key, ChannelID: id}}})
	require.NoError(t, err)
	require.NoError(t, loaded.Items[0].Err)
	var expected replication.ReplicaState
	var previous ch.EntryIdentity
	for previous.Index < uint64(count) {
		// The production default is one MiB, including protocol/identity costs.
		pages := donor.Fetch(ctx, []replication.FetchRange{{ChannelKey: key, ChannelID: id, Expected: loaded.Items[0].State, From: previous.Index + 1, Through: uint64(count), Previous: previous, MaxBytes: 1 << 20}})
		require.Len(t, pages, 1)
		require.NoError(t, pages[0].Err)
		require.NotEmpty(t, pages[0].Proposals)
		for _, proposal := range pages[0].Proposals {
			// Exercise the actual wire envelope as well as native donor reads.
			wire := replication.ExchangeBatch{Version: replication.ExchangeVersion, Items: []replication.ExchangeItem{{RequestID: 1, Kind: replication.ExchangeReplicate, Replicate: &replication.ReplicateRequest{ChannelKey: key, ChannelID: id, Leader: 1, Follower: 2, Manifest: proposal.Manifest, Records: proposal.Records, Committed: proposal.Manifest.LastOffset}}}}
			encoded, err := replication.EncodeExchangeBatch(wire)
			require.NoError(t, err)
			_, err = replication.DecodeExchangeBatch(encoded)
			require.NoError(t, err)
			result := target.Replace(ctx, []replication.RecoveryReplacement{{ChannelKey: key, ChannelID: id, Expected: expected, KeepThrough: previous.Index, Proposals: []replication.RecoveryProposal{proposal}, Committed: proposal.Manifest.LastOffset}})
			require.NoError(t, result[0].Err)
			require.True(t, result[0].Outcome.Durable())
			_, entries, ok := ch.SealProposalManifest(proposal.Manifest, proposal.Records)
			require.True(t, ok)
			previous = entries[len(entries)-1]
			expected = replication.ReplicaState{LEO: previous.Index, Committed: previous.Index, Manifest: proposal.Manifest, TailIdentity: previous}
		}
	}
	log, err := targetFactory.ChannelStore(key, id)
	require.NoError(t, err)
	defer log.Close()
	read, err := log.ReadCommitted(ctx, store.ReadCommittedRequest{FromSeq: 1, Limit: count, MaxBytes: 4 << 20})
	require.NoError(t, err)
	require.Len(t, read.Messages, count)
	for i, message := range read.Messages {
		require.Equal(t, uint64(i+1), message.MessageSeq)
		require.Equal(t, make([]byte, payloadBytes), message.Payload)
	}
}

func TestDirectImportRejectsOversizedMessageBeforeCompletion(t *testing.T) {
	w, plan, report, _ := messageImportPlan(t, "budget-room", 1, (1<<20)-101)
	require.ErrorContains(t, migrationv3.Install(context.Background(), plan, report, w), "native recovery page budget")
	for _, seal := range []string{"MIGRATION-READY", "MIGRATION-COMPLETE"} {
		_, err := os.Stat(filepath.Join(plan.Nodes[0].DataDir, seal))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestOfflineVerifyRejectsCompletedOversizedProposals(t *testing.T) {
	for _, payloadBytes := range []int{32 << 10, 16300} {
		t.Run(fmt.Sprint(payloadBytes), func(t *testing.T) {
			ctx := context.Background()
			w, plan, report, id := messageImportPlan(t, "budget-room", 64, payloadBytes)
			require.NoError(t, migrationv3.Install(ctx, plan, report, w))
			// Reproduce the previous tool's completed generation with one native
			// 64-message proposal. Retain its valid Controller/Slot bootstrap and seals.
			path := filepath.Join(plan.Nodes[0].DataDir, "messages")
			require.NoError(t, os.RemoveAll(path))
			db, err := message.OpenWithLogger(path, nil)
			require.NoError(t, err)
			log, err := db.ForChannel(channelcompat.ChannelKey(ch.ChannelKeyForID(id)), channelcompat.ChannelID{ID: id.ID, Type: id.Type})
			require.NoError(t, err)
			var rows []channelcompat.Record
			var records []quorumlog.Record
			for seq := uint64(1); seq <= 64; seq++ {
				m := channelcompat.Message{MessageID: seq, MessageSeq: seq, ChannelID: id.ID, ChannelType: id.Type, FromUID: "alice", ClientMsgNo: fmt.Sprint(seq), Timestamp: 1700000000, ServerTimestampMS: 1700000000000, Payload: make([]byte, payloadBytes)}
				row, err := message.EncodeMessageRecord(m, 1)
				require.NoError(t, err)
				rows = append(rows, row)
				records = append(records, quorumlog.Record{ID: seq, Index: seq, Epoch: 1, FromUID: m.FromUID, ClientMsgNo: m.ClientMsgNo, ServerTimestampMS: m.ServerTimestampMS, Payload: m.Payload})
			}
			proposal, _, ok := quorumlog.SealProposalManifest(quorumlog.ProposalManifest{Version: quorumlog.ProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: quorumlog.CommandID{1}, LastOffset: 64}, records)
			require.True(t, ok)
			result := message.StoreAppendBatch(ctx, []message.AppendBatchItem{{Store: log, Records: rows, ExactBaseOffset: true, Proposal: proposal, Committed: 64}})
			require.NoError(t, result[0].Err)
			require.True(t, result[0].Outcome.Durable())
			require.NoError(t, log.Close())
			require.NoError(t, db.Close())
			view, err := (migrationv3.Inspector{}).Open(ctx, plan, plan.Nodes[0])
			require.NoError(t, err)
			_, _, err = view.Progress(ctx, migration.ChannelIdentity{ID: id.ID, Type: id.Type})
			require.NoError(t, view.Close())
			require.Error(t, err, "an unrecoverable completed generation must not pass offline verification")
		})
	}
}

func messageImportPlan(t *testing.T, channelID string, count, payloadBytes int) (*transfer.Spool, migration.TargetPlan, migration.TargetRecordsReport, ch.ChannelID) {
	t.Helper()
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "recovery-budget-test", 128<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Close()) })
	id := ch.ChannelID{ID: channelID, Type: 2}
	marshal := func(value any) []byte {
		data, err := migration.MarshalState(value)
		require.NoError(t, err)
		return data
	}
	put := func(key string, value any) {
		require.NoError(t, w.Put(ctx, []transfer.SpoolRow{{Key: []byte(key), Value: marshal(value)}}))
	}
	tuple := migration.IdentityKey(id.ID, id.Type)
	report := migration.TargetRecordsReport{SelectionDigest: strings.Repeat("1", 64), Digest: strings.Repeat("2", 64), Messages: uint64(count), MessageChannels: 1, MaxMessageID: uint64(count)}
	put("conversion/COMPLETE", report)
	put("target/channels/"+tuple, migration.TargetChannel{Channel: migration.ChannelIdentity{ID: id.ID, Type: id.Type}, LastSeq: uint64(count), Count: uint64(count)})
	for seq := 1; seq <= count; seq++ {
		put(fmt.Sprintf("target/messages/%s/%020d", tuple, seq), channelcompat.Message{MessageID: uint64(seq), MessageSeq: uint64(seq), ChannelID: id.ID, ChannelType: id.Type, FromUID: "alice", ClientMsgNo: fmt.Sprint(seq), Timestamp: 1700000000, ServerTimestampMS: 1700000000000, Payload: make([]byte, payloadBytes)})
	}
	plan := migration.TargetPlan{ClusterID: "recovery-budget", CreatedAt: time.Unix(1700000000, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 1, Addr: "127.0.0.1:17001", DataDir: filepath.Join(t.TempDir(), "node")}}}
	return w, plan, report, id
}
