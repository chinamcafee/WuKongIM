package replication_test

import (
	"context"
	"encoding/hex"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

// The source uses the pre-existing compatibility record format and v1 manifest. No
// new row format or proposal digest is needed to retain the existing flag bit.
func TestRedDotExistingRowSurvivesFollowerAndRecoveryReopen(t *testing.T) {
	for _, redDot := range []bool{false, true} {
		ctx := context.Background()
		id := ch.ChannelID{ID: "red-dot-existing", Type: 2}
		key := ch.ChannelKeyForID(id)
		source := channelcompat.Message{MessageID: 991, MessageSeq: 1, ChannelID: id.ID, ChannelType: id.Type,
			ServerTimestampMS: 1700000000000, FromUID: "sender", ClientMsgNo: "client", Payload: []byte{0, 1, 255}}
		source.Framer.RedDot = redDot
		// Fixed pre-change message payload: the existing RedDot header bit is byte 9.
		payload, err := hex.DecodeString("0100000000000003df02000002000000000000000000000000000000000000000000000000d94a37186c0d38bf0000000000000006636c69656e7400000000000000107265642d646f742d6578697374696e67000000000000000673656e646572000000030001ff776b74730000018bcfe56800")
		require.NoError(t, err)
		if !redDot {
			payload[9] = 0
		}
		row := channelcompat.Record{ID: 991, Index: 1, Epoch: 1, Payload: payload, SizeBytes: len(payload)}
		manifest, _, ok := quorumlog.SealProposalManifest(quorumlog.ProposalManifest{
			Version: 1, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: quorumlog.CommandID{1}, LastOffset: 1,
		}, []quorumlog.Record{{ID: 991, Index: 1, Epoch: 1, FromUID: source.FromUID, ClientMsgNo: source.ClientMsgNo, ServerTimestampMS: source.ServerTimestampMS, Payload: source.Payload}})
		require.True(t, ok)
		sourcePath := t.TempDir()
		db, err := message.OpenWithLogger(sourcePath, nil)
		require.NoError(t, err)
		log, err := db.ForChannel(channelcompat.ChannelKey(key), channelcompat.ChannelID{ID: id.ID, Type: id.Type})
		require.NoError(t, err)
		written := message.StoreAppendBatch(ctx, []message.AppendBatchItem{{Store: log, Records: []channelcompat.Record{row}, ExactBaseOffset: true, Proposal: manifest, Committed: 1}})
		require.NoError(t, written[0].Err)
		require.NoError(t, log.Close())
		require.NoError(t, db.Close())
		factory := store.NewMessageDBFactory(sourcePath)
		donor, err := factory.ChannelStore(key, id)
		require.NoError(t, err)
		page, err := donor.(store.ExactRecoveryPageReader).ReadExactRecoveryPage(ctx, store.ExactRecoveryPageRequest{From: 1, Through: 1, MaxBytes: 1 << 20})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
		require.Equal(t, redDot, page.Records[0].RedDot)
		latest, _, _, err := factory.ListLatestMessages(ctx, 0, 10)
		require.NoError(t, err)
		require.Len(t, latest, 1)
		require.Equal(t, redDot, latest[0].RedDot)
		require.NoError(t, donor.Close())
		require.NoError(t, factory.Close())
		wire, err := replication.EncodeExchangeBatchResult(replication.ExchangeBatchResult{Version: replication.ExchangeVersion,
			Items: []replication.ExchangeItemResult{{RequestID: 1, Fetch: replication.FetchResult{Proposals: []replication.RecoveryProposal{{Manifest: manifest, Records: page.Records}}}}}})
		require.NoError(t, err)
		decoded, err := replication.DecodeExchangeBatchResult(wire)
		require.NoError(t, err)
		copied := decoded.Items[0].Fetch.Proposals[0]
		for _, recovery := range []bool{false, true} {
			targetPath := t.TempDir()
			targetFactory := store.NewMessageDBFactory(targetPath)
			target, err := targetFactory.ChannelStore(key, id)
			require.NoError(t, err)
			if recovery {
				result, err := target.(store.RecoverySuffixReplacer).ReplaceRecoverySuffix(ctx, store.ReplaceRecoverySuffixRequest{
					Proposals: []store.RecoveryProposal{{Manifest: copied.Manifest, Records: copied.Records}}, Committed: 1,
				})
				require.NoError(t, err)
				require.True(t, result.Outcome.Durable())
			} else {
				_, err := target.ApplyFollower(ctx, store.ApplyFollowerRequest{Records: copied.Records, LeaderHW: 1})
				require.NoError(t, err)
			}
			require.NoError(t, target.Close())
			require.NoError(t, targetFactory.Close())
			reopened, err := message.OpenWithLogger(targetPath, nil)
			require.NoError(t, err)
			read, err := reopened.ForChannel(channelcompat.ChannelKey(key), channelcompat.ChannelID{ID: id.ID, Type: id.Type})
			require.NoError(t, err)
			got, found, err := read.GetMessageBySeq(1)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, source, got)
			encoded, err := read.Read(0, 1<<20)
			require.NoError(t, err)
			require.Len(t, encoded, 1)
			require.Equal(t, row.Payload, encoded[0].Payload, "existing record bytes must remain unchanged")
			require.NoError(t, read.Close())
			require.NoError(t, reopened.Close())
		}
	}
}
