package message

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func TestOfflineImportedLogVerificationRejectsCorruptInteriorIdentity(t *testing.T) {
	ctx := context.Background()
	engine := openCompatEngine(t)
	id := channel.ChannelID{ID: "offline-import", Type: 1}
	store := mustForChannel(t, engine, "offline-import", id)
	defer store.Close()
	records := []channel.Record{
		compatExactTestRecord(t, 1, 901, id.ID, "first"),
		compatExactTestRecord(t, 1, 902, id.ID, "second"),
	}
	manifest := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: quorumlog.ProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1,
		CommandID: quorumlog.CommandID{1}, LastOffset: 2,
	}, records)
	result := StoreAppendBatch(ctx, []AppendBatchItem{{Store: store, Records: records, ExactBaseOffset: true, Proposal: manifest, Committed: 2}})
	require.Len(t, result, 1)
	require.NoError(t, result[0].Err)
	require.NoError(t, store.log.VerifyOfflineImportedLog(ctx, 1<<20))

	// Counts, payloads and the final frontier remain unchanged. Only a complete
	// proposal walk can detect this non-tail identity's wrong channel epoch.
	entry, found, err := loadDurableEntryIdentityFrom(engine.engine, store.log.key, 1)
	require.NoError(t, err)
	require.True(t, found)
	entry.ChannelEpoch++
	setPhysicalTestValue(t, engine, encodeEntryIdentityKey(store.log.key, 1), encodeDurableEntryIdentity(entry))
	require.ErrorIs(t, store.log.VerifyOfflineImportedLog(ctx, 1<<20), dberrors.ErrCorruptState)
}

func TestOfflineEmptySenderChecksExactClientIndex(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := testChannelLog(store)
	defer log.Close()
	ctx := context.Background()
	_, err := log.Append(ctx, []Record{{ID: 61, ClientMsgNo: "shared", Payload: []byte("one")}, {ID: 62, ClientMsgNo: "shared", Payload: []byte("two")}}, AppendOptions{})
	require.NoError(t, err)
	for seq := uint64(1); seq <= 2; seq++ {
		row, found, err := log.ReadOfflineMessage(ctx, seq)
		require.NoError(t, err)
		require.True(t, found)
		require.EqualValues(t, seq, row.MessageSeq)
	}
	setPhysicalTestValue(t, &Engine{engine: store.engine}, encodeMessageClientMsgNoIndexKey(log.key, "shared", 1), encodeMessageIDIndexValue(2))
	_, _, err = log.ReadOfflineMessage(ctx, 1)
	require.ErrorIs(t, err, dberrors.ErrCorruptState)
	_, found, err := log.ReadOfflineMessage(ctx, 2)
	require.NoError(t, err)
	require.True(t, found)
}
