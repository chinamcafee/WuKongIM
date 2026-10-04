package cluster

import (
	"context"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

type linkuUnreadNode struct {
	conversationNodeFake
	messages []channelruntime.Message
}

func (n *linkuUnreadNode) ReadChannelCommitted(_ context.Context, _ channelruntime.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	var messages []channelruntime.Message
	for _, m := range n.messages {
		if m.MessageSeq >= req.FromSeq && m.MessageSeq <= req.MaxSeq {
			messages = append(messages, m)
		}
	}
	return channelstore.ReadCommittedResult{Messages: messages}, nil
}
func TestLinkUUnreadCountsRedDotsWithoutSelfSendReset(t *testing.T) {
	node := &linkuUnreadNode{conversationNodeFake: conversationNodeFake{heads: []clusterchannels.ConversationHeadResult{{Head: clusterchannels.ConversationHead{ReadThroughSeq: 6, CurrentUserLastSendSeq: 4, Found: true, Message: channelruntime.Message{MessageSeq: 6}}}}}, messages: []channelruntime.Message{
		{MessageSeq: 1, FromUID: "other", RedDot: true}, {MessageSeq: 2, FromUID: "other"}, {MessageSeq: 3, FromUID: "other", RedDot: true, SyncOnce: true}, {MessageSeq: 4, FromUID: "me", RedDot: true}, {MessageSeq: 5, FromUID: "other", RedDot: true}, {MessageSeq: 6, FromUID: "other", RedDot: true},
	}}
	rows := []metadb.UserChannelMembership{{UID: "me", ChannelID: "g", ChannelType: 2}}
	heads, err := NewConversationStore(node).HydrateConversationHeads(context.Background(), "me", rows, 1)
	require.NoError(t, err)
	require.Zero(t, heads[0].CurrentUserLastSendSeq)
	require.Equal(t, uint64(3), heads[0].NonBusinessUnread)
	require.True(t, heads[0].BoundaryComputed)
	require.Equal(t, uint64(5), heads[0].UnreadBoundary)
	rows[0].ReadSeq = 1
	heads, err = NewConversationStore(node).HydratePersistedConversationHeads(context.Background(), "me", rows)
	require.NoError(t, err)
	require.Equal(t, uint64(3), heads[0].NonBusinessUnread)
}
