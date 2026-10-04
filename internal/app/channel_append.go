package app

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
)

// channelAppendAuthorityLocal admits RPC-forwarded sends to the local authority reactor.
type channelAppendAuthorityLocal struct {
	group *channelappend.Group
}

func (l channelAppendAuthorityLocal) SubmitForAuthority(ctx context.Context, target channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	if l.group == nil {
		return channelAppendErrorResults(len(items), channelappend.ErrRouteNotReady)
	}
	future, err := l.group.SubmitLocal(ctx, target, items)
	if err != nil {
		return channelAppendErrorResults(len(items), err)
	}
	results, err := future.Wait(ctx)
	if err != nil {
		return channelAppendErrorResults(len(items), err)
	}
	if len(results) != len(items) {
		return channelAppendErrorResults(len(items), channelappend.ErrAppendResultMissing)
	}
	return results
}

// channelAppendSubscriberSource pages current Slot-leader subscribers. Versioned
// snapshot reuse belongs to channelappend, never the benchmark setup cache.
type channelAppendSubscriberSource struct {
	node recipientSubscriberNode
}

func (s channelAppendSubscriberSource) NextSubscriberPage(ctx context.Context, req channelappend.SubscriberPageRequest) (channelappend.SubscriberPage, error) {
	if s.node == nil {
		return channelappend.SubscriberPage{Done: true}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 1
	}
	uids, cursor, done, err := s.node.ListChannelSubscribersAuthoritative(ctx, req.ChannelID.ID, int64(req.ChannelID.Type), req.Cursor, limit)
	if err != nil {
		return channelappend.SubscriberPage{}, err
	}
	recipients := make([]channelappend.Recipient, 0, len(uids))
	for _, uid := range uids {
		if uid != "" {
			recipients = append(recipients, channelappend.Recipient{UID: uid})
		}
	}
	return channelappend.SubscriberPage{Recipients: recipients, Cursor: cursor, Done: done}, nil
}

func channelAppendErrorResults(n int, err error) []channelappend.SendBatchItemResult {
	results := make([]channelappend.SendBatchItemResult, n)
	for i := range results {
		results[i].Err = err
	}
	return results
}

// legacyCMDProjectionNode retains the historical cursor table behind replicated Slot writes.
type legacyCMDProjectionNode interface {
	UpsertCMDConversationStatesBatch(context.Context, []metadb.CMDConversationState) error
}
type legacyCMDProjector struct{ node legacyCMDProjectionNode }

// ProjectCMDRecipients is independent of online presence and individual device ACKs.
func (p legacyCMDProjector) ProjectCMDRecipients(ctx context.Context, event channelappend.CommittedEnvelope, recipients []channelappend.Recipient) error {
	states := make([]metadb.CMDConversationState, 0, len(recipients))
	seen := make(map[string]struct{}, len(recipients))
	for _, recipient := range recipients {
		if recipient.UID == "" {
			continue
		}
		if _, ok := seen[recipient.UID]; ok {
			continue
		}
		seen[recipient.UID] = struct{}{}
		states = append(states, metadb.CMDConversationState{UID: recipient.UID, ChannelID: event.ChannelID, ChannelType: int64(event.ChannelType), ActiveAt: event.ServerTimestampMS, UpdatedAt: event.ServerTimestampMS})
	}
	if len(states) == 0 {
		return nil
	}
	return p.node.UpsertCMDConversationStatesBatch(ctx, states)
}
