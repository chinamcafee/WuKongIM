package meta

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLinkULegacyMembershipFloorsAndCMDDiscovery(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	s := store.db.HashSlot(15)
	ctx := context.Background()
	require.NoError(t, s.UpsertUserChannelMembership(ctx, UserChannelMembership{UID: "u", ChannelID: "g", ChannelType: 2, JoinSeq: 1, UpdatedAt: 10}))
	require.NoError(t, s.UpsertConversationState(ctx, ConversationState{UID: "u", Kind: ConversationKindNormal, ChannelID: "g", ChannelType: 2, ReadSeq: 7, DeletedToSeq: 4, ActiveAt: 100, UpdatedAt: 100}))
	row, ok, err := s.GetUserChannelMembership(ctx, "u", "g", 2)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(7), row.ReadSeq)
	require.Equal(t, uint64(4), row.DeletedToSeq)
	page, _, _, err := s.ListUserChannelMembershipPage(ctx, "u", UserChannelMembershipCursor{}, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(7), page[0].ReadSeq)
	for _, id := range []string{"a_cmd", "c_cmd"} {
		require.NoError(t, s.UpsertConversationState(ctx, ConversationState{UID: "u", Kind: ConversationKindCMD, ChannelID: id, ChannelType: 2, ReadSeq: 3, ActiveAt: 100, UpdatedAt: 100}))
	}
	require.NoError(t, s.UpsertUserCMDChannelMembership(ctx, UserCMDChannelMembership{UID: "u", CommandChannelID: "b_cmd", ChannelType: 2, StartSeq: 5, UpdatedAt: 101}))
	var cursor UserCMDChannelMembershipCursor
	for _, id := range []string{"a_cmd", "b_cmd", "c_cmd"} {
		rows, next, _, err := s.ListUserCMDChannelMembershipPage(ctx, "u", cursor, 1)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, id, rows[0].CommandChannelID)
		cursor = next
	}
	require.NoError(t, s.UpsertUserCMDChannelMembership(ctx, UserCMDChannelMembership{UID: "u", CommandChannelID: "a_cmd", ChannelType: 2, Tombstone: true, TombstoneAt: 200, UpdatedAt: 200}))
	rows, _, _, err := s.ListUserCMDChannelMembershipPage(ctx, "u", UserCMDChannelMembershipCursor{}, 10)
	require.NoError(t, err)
	require.True(t, rows[0].Tombstone)
	// A new join generation must not inherit old read/delete state.
	require.NoError(t, s.UpsertUserChannelMembership(ctx, UserChannelMembership{UID: "u", ChannelID: "g", ChannelType: 2, JoinSeq: 20, SourceVersion: 2, UpdatedAt: 200}))
	row, _, err = s.GetUserChannelMembership(ctx, "u", "g", 2)
	require.NoError(t, err)
	require.Equal(t, uint64(0), row.ReadSeq)
}
