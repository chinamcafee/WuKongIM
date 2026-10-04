package meta

import (
	"context"
	"sort"
)

// withLegacyConversationFloors reads historical Link-U cursor state without
// mutating replicas outside the Slot FSM. A newer membership generation wins.
func (s *Shard) withLegacyConversationFloors(ctx context.Context, row UserChannelMembership) (UserChannelMembership, error) {
	old, ok, err := s.GetConversationState(ctx, ConversationKindNormal, row.UID, row.ChannelID, row.ChannelType)
	if err != nil {
		return row, err
	}
	if ok && !row.Tombstone && old.UpdatedAt >= row.UpdatedAt {
		row.ReadSeq = max(row.ReadSeq, old.ReadSeq)
		row.DeletedToSeq = max(row.DeletedToSeq, old.DeletedToSeq)
	}
	return row, nil
}

// ListUserCMDChannelMembershipPage merges old discovery rows and the new
// directory in stable key order. Explicit new bindings, including tombstones,
// shadow old rows; device ACKs remain in their separate table.
func (s *Shard) ListUserCMDChannelMembershipPage(ctx context.Context, uid string, cursor UserCMDChannelMembershipCursor, limit int) ([]UserCMDChannelMembership, UserCMDChannelMembershipCursor, bool, error) {
	rows, _, nativeDone, err := s.listUserCMDChannelMembershipPageNative(ctx, uid, cursor, limit)
	if err != nil {
		return nil, UserCMDChannelMembershipCursor{}, false, err
	}
	legacy, _, legacyDone, err := s.ListConversationStatePage(ctx, ConversationKindCMD, uid, ConversationCursor{ChannelID: cursor.CommandChannelID, ChannelType: cursor.ChannelType}, limit)
	if err != nil {
		return nil, UserCMDChannelMembershipCursor{}, false, err
	}
	byKey := make(map[ChannelKey]UserCMDChannelMembership, len(rows)+len(legacy))
	for _, row := range legacy {
		byKey[ChannelKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}] = UserCMDChannelMembership{UID: uid, CommandChannelID: row.ChannelID, ChannelType: row.ChannelType, StartSeq: 1, AckSeq: row.ReadSeq, UpdatedAt: row.UpdatedAt}
	}
	for _, row := range rows {
		byKey[ChannelKey{ChannelID: row.CommandChannelID, ChannelType: row.ChannelType}] = row
	}
	merged := make([]UserCMDChannelMembership, 0, len(byKey))
	for _, row := range byKey {
		merged = append(merged, row)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].CommandChannelID != merged[j].CommandChannelID {
			return merged[i].CommandChannelID < merged[j].CommandChannelID
		}
		return merged[i].ChannelType < merged[j].ChannelType
	})
	done := nativeDone && legacyDone && len(merged) <= limit
	if len(merged) > limit {
		merged = merged[:limit]
	}
	next := cursor
	if len(merged) != 0 {
		last := merged[len(merged)-1]
		next = UserCMDChannelMembershipCursor{CommandChannelID: last.CommandChannelID, ChannelType: last.ChannelType}
	}
	return merged, next, done, nil
}
