package node

import (
	"context"
	"fmt"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// Owner snapshot replies have their own versioned layout; existing presence
// response bytes and operation IDs remain unchanged during rolling upgrades.
var ownerRoutesMagic = []byte{'W', 'K', 'V', 'O', 1}

func (a *Adapter) handleOwnerRoutes(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	var snapshot presence.OwnerRouteSnapshot
	var err error
	if a == nil || a.ownerRoutes == nil || len(req.EndpointGroups) != 1 || len(req.EndpointGroups[0].UIDs) > authority.RecoveryBatchSize {
		err = authority.ErrRouteNotReady
	} else {
		group := req.EndpointGroups[0]
		snapshot, err = a.ownerRoutes.ReadOwnerRoutes(ctx, group.Target, group.UIDs)
	}
	if len(snapshot.Routes) > maxPresenceRPCCollectionLen {
		snapshot = presence.OwnerRouteSnapshot{}
		err = authority.ErrRouteNotReady
	}
	dst := append([]byte(nil), ownerRoutesMagic...)
	dst = appendString(dst, presenceRPCStatusForError(err))
	dst = appendUvarint(dst, snapshot.OwnerNodeID)
	dst = appendUvarint(dst, snapshot.OwnerBootID)
	return appendPresenceRoutes(dst, snapshot.Routes), nil
}

// ReadOwnerRoutes obtains one complete active-route page from an exact owner.
func (c *Client) ReadOwnerRoutes(ctx context.Context, owner uint64, target presence.RouteTarget, uids []string) (presence.OwnerRouteSnapshot, error) {
	if c == nil || c.node == nil || owner == 0 || len(uids) > authority.RecoveryBatchSize {
		return presence.OwnerRouteSnapshot{}, authority.ErrRouteNotReady
	}
	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: presenceOpReadOwnerRoutes, EndpointGroups: []presence.EndpointLookupGroup{{Target: target, UIDs: uids}}})
	if err != nil {
		return presence.OwnerRouteSnapshot{}, err
	}
	reply, err := c.node.CallRPC(ctx, owner, PresenceOwnerRPCServiceID, body)
	if err != nil {
		return presence.OwnerRouteSnapshot{}, err
	}
	return decodeOwnerRoutes(reply)
}

func decodeOwnerRoutes(body []byte) (presence.OwnerRouteSnapshot, error) {
	if !hasMagic(body, ownerRoutesMagic) {
		return presence.OwnerRouteSnapshot{}, fmt.Errorf("internal/access/node: invalid owner routes codec")
	}
	offset := len(ownerRoutesMagic)
	status, offset, err := readString(body, offset)
	if err != nil {
		return presence.OwnerRouteSnapshot{}, err
	}
	nodeID, offset, err := readUvarint(body, offset)
	if err != nil {
		return presence.OwnerRouteSnapshot{}, err
	}
	bootID, offset, err := readUvarint(body, offset)
	if err != nil {
		return presence.OwnerRouteSnapshot{}, err
	}
	routes, offset, err := readPresenceRoutes(body, offset)
	if err != nil {
		return presence.OwnerRouteSnapshot{}, err
	}
	if offset != len(body) {
		return presence.OwnerRouteSnapshot{}, fmt.Errorf("internal/access/node: trailing owner routes bytes")
	}
	if err := presenceRPCErrorForStatus(status); err != nil {
		return presence.OwnerRouteSnapshot{}, err
	}
	return presence.OwnerRouteSnapshot{OwnerNodeID: nodeID, OwnerBootID: bootID, Routes: routes}, nil
}
