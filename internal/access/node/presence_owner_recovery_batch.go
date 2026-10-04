package node

import (
	"context"
	"fmt"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// Operation 10 and WKVO version 2 add aligned owner reads; operation 9 keeps
// its exact singleton request and response bytes for compatibility.
var ownerRoutesBatchMagic = []byte{'W', 'K', 'V', 'O', 2}

func (a *Adapter) handleOwnerRoutesByTargets(ctx context.Context, req presenceRPCRequest) ([]byte, error) {
	if !presence.ValidOwnerRecoveryPage(req.EndpointGroups) {
		return nil, authority.ErrRouteNotReady
	}
	results := make([]presence.OwnerRouteResult, len(req.EndpointGroups))
	var reader presence.OwnerRouteBatchReader
	if a != nil {
		reader, _ = a.ownerRoutes.(presence.OwnerRouteBatchReader)
	}
	if reader == nil {
		for i := range results {
			results[i].Err = authority.ErrRouteNotReady
		}
	} else {
		results = reader.ReadOwnerRoutesByTargets(ctx, req.EndpointGroups)
	}
	if len(results) != len(req.EndpointGroups) {
		return nil, authority.ErrRouteNotReady
	}
	total := 0
	dst := append([]byte(nil), ownerRoutesBatchMagic...)
	dst = appendUvarint(dst, uint64(len(results)))
	for _, result := range results {
		total += len(result.Snapshot.Routes)
		if total > 4096 {
			return nil, authority.ErrRouteNotReady
		}
		dst = appendString(dst, presenceRPCStatusForError(result.Err))
		dst = appendUvarint(dst, result.Snapshot.OwnerNodeID)
		dst = appendUvarint(dst, result.Snapshot.OwnerBootID)
		dst = appendPresenceRoutes(dst, result.Snapshot.Routes)
	}
	return dst, nil
}

// ReadOwnerRoutesByTargets performs one owner RPC for a bounded multi-Slot page.
// Unsupported or malformed replies fail explicitly; they never become offline proof.
func (c *Client) ReadOwnerRoutesByTargets(ctx context.Context, owner uint64, groups []presence.EndpointLookupGroup) ([]presence.OwnerRouteResult, error) {
	if c == nil || c.node == nil || owner == 0 || !presence.ValidOwnerRecoveryPage(groups) {
		return nil, authority.ErrRouteNotReady
	}
	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: presenceOpReadOwnerRoutesByTargets, EndpointGroups: groups})
	if err != nil {
		return nil, err
	}
	reply, err := c.node.CallRPC(ctx, owner, PresenceOwnerRPCServiceID, body)
	if err != nil {
		return nil, err
	}
	results, err := decodeOwnerRoutesByTargets(reply)
	if err != nil {
		return nil, err
	}
	if len(results) != len(groups) {
		return nil, fmt.Errorf("internal/access/node: misaligned owner route results")
	}
	return results, nil
}

func decodeOwnerRoutesByTargets(body []byte) ([]presence.OwnerRouteResult, error) {
	if !hasMagic(body, ownerRoutesBatchMagic) {
		return nil, fmt.Errorf("internal/access/node: invalid owner routes batch codec")
	}
	count, offset, err := readUvarint(body, len(ownerRoutesBatchMagic))
	if err != nil {
		return nil, err
	}
	if count == 0 || count > 256 {
		return nil, fmt.Errorf("internal/access/node: owner routes batch count out of bounds")
	}
	results := make([]presence.OwnerRouteResult, int(count))
	total := 0
	for i := range results {
		var status string
		status, offset, err = readString(body, offset)
		if err != nil {
			return nil, err
		}
		results[i].Err = presenceRPCErrorForStatus(status)
		results[i].Snapshot.OwnerNodeID, offset, err = readUvarint(body, offset)
		if err != nil {
			return nil, err
		}
		results[i].Snapshot.OwnerBootID, offset, err = readUvarint(body, offset)
		if err != nil {
			return nil, err
		}
		results[i].Snapshot.Routes, offset, err = readPresenceRoutes(body, offset)
		if err != nil {
			return nil, err
		}
		total += len(results[i].Snapshot.Routes)
		if total > 4096 {
			return nil, fmt.Errorf("internal/access/node: owner routes batch output too large")
		}
	}
	if offset != len(body) {
		return nil, fmt.Errorf("internal/access/node: trailing owner routes batch bytes")
	}
	return results, nil
}
