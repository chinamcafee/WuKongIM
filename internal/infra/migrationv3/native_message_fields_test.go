package migrationv3_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

// This private storage experiment deliberately bypasses migration admission to
// demonstrate why a successful row write is insufficient compatibility proof.
// It neither changes native storage nor certifies a cluster migration.
func TestNativeFollowerRoundTripMessageFieldCompatibility(t *testing.T) {
	for _, field := range []string{"red_dot", "stream_no"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			id := ch.ChannelID{ID: "compatibility-probe", Type: 2}
			key := ch.ChannelKeyForID(id)
			source := channelcompat.Message{MessageID: 991, MessageSeq: 1, ChannelID: id.ID, ChannelType: id.Type, Timestamp: 1700000000, ServerTimestampMS: 1700000000000, FromUID: "synthetic-user", ClientMsgNo: "synthetic-client", Payload: []byte{0, 1, 255}}
			if field == "red_dot" {
				source.Framer.RedDot = true
			} else {
				source.StreamNo = "synthetic-stream"
				source.Setting = 2 // The native stream bit survives even though StreamNo is lost.
			}
			_, _, err := migration.PrepareMessageRecord(source)
			if field == "red_dot" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, field)
			}
			path := filepath.Join(t.TempDir(), "source")
			db, err := message.OpenWithLogger(path, nil)
			require.NoError(t, err)
			log, err := db.ForChannel(channelcompat.ChannelKey(key), channelcompat.ChannelID{ID: id.ID, Type: id.Type})
			require.NoError(t, err)
			row, err := message.EncodeMessageRecord(source, 1)
			require.NoError(t, err)
			record := quorumlog.Record{ID: source.MessageID, Index: 1, Epoch: 1, FromUID: source.FromUID, ClientMsgNo: source.ClientMsgNo, ServerTimestampMS: source.ServerTimestampMS, Setting: uint8(source.Setting), Payload: source.Payload}
			manifest, _, ok := quorumlog.SealProposalManifest(quorumlog.ProposalManifest{Version: 1, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: quorumlog.CommandID{1}, LastOffset: 1}, []quorumlog.Record{record})
			require.True(t, ok)
			writes := message.StoreAppendBatch(ctx, []message.AppendBatchItem{{Store: log, Records: []channelcompat.Record{row}, ExactBaseOffset: true, Proposal: manifest, Committed: 1}})
			require.NoError(t, writes[0].Err)
			require.True(t, writes[0].Outcome.Durable())
			original, found, err := log.GetMessageBySeq(1)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, source.Framer.RedDot, original.Framer.RedDot)
			require.Equal(t, source.StreamNo, original.StreamNo)
			require.NoError(t, log.Close())
			require.NoError(t, db.Close())
			factory := store.NewMessageDBFactory(path)
			donor, err := factory.ChannelStore(key, id)
			require.NoError(t, err)
			records, err := donor.ReadLog(ctx, store.ReadLogRequest{FromOffset: 1, MaxOffset: 1, MaxBytes: 1 << 20})
			require.NoError(t, err)
			require.Len(t, records.Records, 1)
			targetPath := filepath.Join(t.TempDir(), "target")
			targetFactory := store.NewMessageDBFactory(targetPath)
			target, err := targetFactory.ChannelStore(key, id)
			require.NoError(t, err)
			_, err = target.ApplyFollower(ctx, store.ApplyFollowerRequest{Records: records.Records, LeaderHW: 1})
			require.NoError(t, err)
			require.NoError(t, target.Close())
			require.NoError(t, targetFactory.Close())
			require.NoError(t, donor.Close())
			require.NoError(t, factory.Close())
			db, err = message.OpenWithLogger(targetPath, nil)
			require.NoError(t, err)
			defer db.Close()
			log, err = db.ForChannel(channelcompat.ChannelKey(key), channelcompat.ChannelID{ID: id.ID, Type: id.Type})
			require.NoError(t, err)
			defer log.Close()
			replicated, found, err := log.GetMessageBySeq(1)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, source.MessageID, replicated.MessageID)
			require.Equal(t, source.MessageSeq, replicated.MessageSeq)
			require.Equal(t, source.Payload, replicated.Payload)
			require.Equal(t, source.Setting, replicated.Setting)
			require.Equal(t, source.Framer.RedDot, replicated.Framer.RedDot)
			require.Empty(t, replicated.StreamNo)
		})
	}
}
