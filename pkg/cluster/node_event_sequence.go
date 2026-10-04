package cluster

import (
	"context"
	"encoding/json"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ListMessageEventStatesBySequence routes a bounded durable projection read to
// the current Slot leader. Cache-only uncommitted deltas are never fabricated
// into historical events or returned with invented sequence numbers.
func (n *Node) ListMessageEventStatesBySequence(ctx context.Context, q metadb.MessageEventSequenceQuery) ([]metadb.MessageEventState, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	route, err := n.RouteKey(q.ChannelID)
	if err != nil {
		return nil, err
	}
	if route.Leader == 0 {
		return nil, ErrNoSlotLeader
	}
	if route.Leader == n.cfg.NodeID {
		return n.listMessageEventStatesBySequenceLocal(ctx, q)
	}
	body, err := json.Marshal(messageEventAppendRPCRequest{Op: "sequence", Sequence: &q})
	if err != nil {
		return nil, err
	}
	data, err := n.CallRPC(ctx, route.Leader, clusternet.RPCMessageEventAppend, body)
	if err != nil {
		return nil, err
	}
	var response messageEventAppendRPCResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return response.Events, nil
}

func (n *Node) listMessageEventStatesBySequenceLocal(ctx context.Context, q metadb.MessageEventSequenceQuery) ([]metadb.MessageEventState, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, ErrNotStarted
	}
	route, err := n.RouteKey(q.ChannelID)
	if err != nil {
		return nil, err
	}
	if route.Leader != n.cfg.NodeID {
		return nil, ErrNotLeader
	}
	return n.defaultSlotMetaDB.MetaDB().HashSlot(route.HashSlot).ListMessageEventStatesBySequence(ctx, q)
}
