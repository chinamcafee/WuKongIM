package meta

import (
	"bytes"
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// ImportMessageEventProjection installs one exact historical lane, its latest
// idempotency result and the complete message cursor without replaying events.
// The migration coordinator must own an isolated empty target. Exact retries
// are accepted; changed state or a cursor advanced by foreground writes fails.
// Multiple lanes may share the same exact cursor, keeping each call bounded.
func (s *Shard) ImportMessageEventProjection(ctx context.Context, state MessageEventState, cursor MessageEventCursor) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := ValidateImportedMessageEvent(state, cursor); err != nil {
		return err
	}
	applied := MessageEventApplied{ChannelID: state.ChannelID, ChannelType: state.ChannelType, ClientMsgNo: state.ClientMsgNo, EventID: state.LastEventID, EventKey: state.EventKey, MsgEventSeq: state.LastMsgEventSeq, Status: state.Status, UpdatedAt: state.UpdatedAt}
	stateKey, err := messageEventStateRowKey(s.hashSlot, state.ChannelID, state.ChannelType, state.ClientMsgNo, state.EventKey)
	if err != nil {
		return err
	}
	cursorKey, err := messageEventCursorRowKey(s.hashSlot, cursor.ChannelID, cursor.ChannelType, cursor.ClientMsgNo)
	if err != nil {
		return err
	}
	appliedKey, err := messageEventAppliedRowKey(s.hashSlot, applied.ChannelID, applied.ChannelType, applied.ClientMsgNo, applied.EventID)
	if err != nil {
		return err
	}
	rows := []struct{ key, value []byte }{
		{stateKey, encodeMessageEventStateValue(state)},
		{cursorKey, encodeMessageEventCursorValue(cursor)},
		{appliedKey, encodeMessageEventAppliedValue(applied)},
	}
	unlock := s.lock()
	defer unlock()
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	for _, row := range rows {
		existing, ok, err := s.db.get(row.key)
		if err != nil {
			return err
		}
		if ok {
			if !bytes.Equal(existing, row.value) {
				return dberrors.ErrConflict
			}
			continue
		}
		if err := batch.Set(row.key, row.value); err != nil {
			return err
		}
	}
	return batch.Commit(true)
}

// ValidateImportedMessageEvent checks whether the native projection, cursor,
// retry identity and sequence-read byte bound can preserve an original lane.
// It performs no storage access and is suitable for offline preflight.
func ValidateImportedMessageEvent(state MessageEventState, cursor MessageEventCursor) error {
	channelID, clientMsgNo, eventKey, err := normalizeMessageEventStateKey(state.ChannelID, state.ChannelType, state.ClientMsgNo, state.EventKey)
	if err != nil {
		return err
	}
	if channelID != state.ChannelID || clientMsgNo != state.ClientMsgNo || eventKey != state.EventKey || state.LastEventID == "" || state.LastEventType == "" || state.LastMsgEventSeq == 0 || cursor.LastMsgEventSeq < state.LastMsgEventSeq || cursor.LastMsgEventSeq == math.MaxUint64 || cursor.ChannelID != state.ChannelID || cursor.ChannelType != state.ChannelType || cursor.ClientMsgNo != state.ClientMsgNo {
		return dberrors.ErrInvalidArgument
	}
	if err := validateMessageEventState(state); err != nil {
		return err
	}
	if err := validateMessageEventCursor(cursor); err != nil {
		return err
	}
	applied := MessageEventApplied{
		ChannelID: state.ChannelID, ChannelType: state.ChannelType, ClientMsgNo: state.ClientMsgNo,
		EventID: state.LastEventID, EventKey: state.EventKey, MsgEventSeq: state.LastMsgEventSeq, Status: state.Status, UpdatedAt: state.UpdatedAt,
	}
	if err := validateMessageEventApplied(applied); err != nil {
		return err
	}
	if eventStateSize(state) > MaxMessageEventPageBytes {
		return dberrors.ErrInvalidArgument
	}
	return nil
}
