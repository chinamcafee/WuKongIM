package meta

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// MaxMessageEventPageBytes bounds retained projected event rows per RPC page.
const MaxMessageEventPageBytes = 8 << 20

// MessageEventSequenceQuery selects current durable lane projections in
// message-event sequence order. It never requests an event history replay.
type MessageEventSequenceQuery struct {
	ChannelID   string
	ChannelType int64
	ClientMsgNo string
	AfterSeq    uint64
	EventKey    string
	// Limit includes any caller-requested lookahead; at most 2001 rows.
	Limit int
}

// ListMessageEventStatesBySequence scans a pinned iterator and keeps only the
// earliest requested sequences in a bounded heap. Native event-key ordering
// must not truncate the rows before applying the sequence cursor.
func (s *Shard) ListMessageEventStatesBySequence(ctx context.Context, q MessageEventSequenceQuery) ([]MessageEventState, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	channel, client, err := normalizeMessageEventMessageKey(q.ChannelID, q.ChannelType, q.ClientMsgNo)
	if err != nil {
		return nil, err
	}
	if q.Limit < 1 || q.Limit > 2001 {
		return nil, dberrors.ErrInvalidArgument
	}
	eventKey := strings.TrimSpace(q.EventKey)
	prefix := KeyParts{String(channel), Int64Ordered(q.ChannelType), String(client)}
	if eventKey != "" {
		prefix = append(prefix, String(eventKey))
	}
	base := encodeRowPrefix(s.hashSlot, messageEventStateTable.spec.ID)
	encoded, err := encodeKeyParts(base, prefix)
	if err != nil {
		return nil, err
	}
	span := keycodec.NewPrefixSpan(encoded)
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	rows := eventSequenceHeap{}
	retainedBytes := 0
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pk, valid := messageEventStateTable.decodePrimaryRowKey(base, iter.Key())
		if !valid || !keyPartsHasPrefix(pk, prefix) {
			return nil, dberrors.ErrCorruptValue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		state, err := messageEventStateTable.decodeValue(iter.Key(), pk, value)
		if err != nil {
			return nil, err
		}
		if state.LastMsgEventSeq <= q.AfterSeq {
			continue
		}
		if len(rows) == q.Limit {
			if !eventSequenceLess(state, rows[0]) {
				continue
			}
			old := heap.Pop(&rows).(MessageEventState)
			retainedBytes -= eventStateSize(old)
		}
		retainedBytes += eventStateSize(state)
		if retainedBytes > MaxMessageEventPageBytes {
			return nil, fmt.Errorf("%w: event projection page exceeds byte budget; lower the limit", dberrors.ErrInvalidArgument)
		}
		heap.Push(&rows, cloneMessageEventState(state))
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return eventSequenceLess(rows[i], rows[j]) })
	return []MessageEventState(rows), nil
}

func eventStateSize(s MessageEventState) int {
	return 256 + len(s.ChannelID) + len(s.ClientMsgNo) + len(s.EventKey) + len(s.Status) + len(s.LastEventID) + len(s.LastEventType) + len(s.LastVisibility) + len(s.SnapshotPayload) + len(s.Error)
}
func eventSequenceLess(a, b MessageEventState) bool {
	if a.LastMsgEventSeq != b.LastMsgEventSeq {
		return a.LastMsgEventSeq < b.LastMsgEventSeq
	}
	return a.EventKey < b.EventKey
}

type eventSequenceHeap []MessageEventState

func (h eventSequenceHeap) Len() int           { return len(h) }
func (h eventSequenceHeap) Less(i, j int) bool { return eventSequenceLess(h[j], h[i]) }
func (h eventSequenceHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *eventSequenceHeap) Push(x any)        { *h = append(*h, x.(MessageEventState)) }
func (h *eventSequenceHeap) Pop() any {
	rows := *h
	last := rows[len(rows)-1]
	rows[len(rows)-1] = MessageEventState{}
	*h = rows[:len(rows)-1]
	return last
}
