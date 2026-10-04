package message

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// MessageEventSequenceReader reads one current durable projection per lane,
// ordered by message event sequence with a bounded result size.
type MessageEventSequenceReader interface {
	ListMessageEventStatesBySequence(context.Context, MessageEventMessageKey, uint64, string, int) ([]MessageEventState, error)
}

// MessageEventSyncQuery preserves the original v2 projected-event cursor.
type MessageEventSyncQuery struct {
	ChannelID       string
	ChannelType     int64
	FromUID         string
	ClientMsgNo     string
	EventKey        string
	FromMsgEventSeq uint64
	Limit           int
	IncludePrivate  bool
}

// SyncedMessageEvent is a current lane projection in the v2 API envelope.
type SyncedMessageEvent struct {
	Seq        uint64
	ID         string
	Key        string
	Type       string
	Visibility string
	OccurredAt int64
	Payload    []byte
}

type MessageEventSyncResult struct {
	ClientMsgNo                      string
	FromMsgEventSeq, NextMsgEventSeq uint64
	More                             bool
	EventKey                         string
	Events                           []SyncedMessageEvent
}

// SyncMessageEvents implements original v2's durable projected-event reads,
// including filtering after the bounded sequence page. It does not change the
// original base message's stream flags or replay past events.
func (a *App) SyncMessageEvents(ctx context.Context, q MessageEventSyncQuery) (MessageEventSyncResult, error) {
	out := MessageEventSyncResult{ClientMsgNo: q.ClientMsgNo, FromMsgEventSeq: q.FromMsgEventSeq, NextMsgEventSeq: q.FromMsgEventSeq, EventKey: q.EventKey, Events: []SyncedMessageEvent{}}
	if strings.TrimSpace(q.ChannelID) == "" {
		return out, ErrMessageEventChannelIDRequired
	}
	if strings.TrimSpace(q.ClientMsgNo) == "" {
		return out, ErrMessageEventClientMsgNoRequired
	}
	if q.ChannelType <= 0 || q.ChannelType > 255 || q.Limit < 0 {
		return out, errors.New("invalid message event sync query")
	}
	if q.Limit == 0 {
		q.Limit = 200
	}
	if q.Limit > 2000 {
		q.Limit = 2000
	}
	if a == nil {
		return out, ErrMessageEventStoreRequired
	}
	reader, ok := a.eventStore.(MessageEventSequenceReader)
	if !ok {
		return out, ErrMessageEventStoreRequired
	}
	channel := q.ChannelID
	if q.ChannelType == int64(channelTypePerson) && strings.TrimSpace(q.FromUID) != "" {
		var err error
		channel, err = runtimechannelid.NormalizePersonChannel(q.FromUID, q.ChannelID)
		if err != nil {
			return out, err
		}
	}
	states, err := reader.ListMessageEventStatesBySequence(ctx, MessageEventMessageKey{ChannelID: channel, ChannelType: q.ChannelType, ClientMsgNo: q.ClientMsgNo}, q.FromMsgEventSeq, q.EventKey, q.Limit+1)
	if err != nil {
		return out, err
	}
	for _, s := range states {
		if !q.IncludePrivate && (s.LastVisibility == VisibilityPrivate || s.LastVisibility == VisibilityRestricted) {
			continue
		}
		if len(out.Events) == q.Limit {
			out.More = true
			break
		}
		eventType := s.LastEventType
		if strings.TrimSpace(eventType) == "" {
			eventType = EventTypeStreamSnapshot
		}
		payload := s.SnapshotPayload
		if len(payload) == 0 {
			fallback := map[string]any{"status": s.Status, "end_reason": s.EndReason}
			if s.Error != "" {
				fallback["error"] = s.Error
			}
			payload, err = json.Marshal(fallback)
			if err != nil {
				return out, err
			}
		}
		out.Events = append(out.Events, SyncedMessageEvent{Seq: s.LastMsgEventSeq, ID: s.LastEventID, Key: s.EventKey, Type: eventType, Visibility: s.LastVisibility, OccurredAt: s.LastOccurredAt, Payload: cloneBytes(payload)})
		out.NextMsgEventSeq = s.LastMsgEventSeq
	}
	return out, nil
}
