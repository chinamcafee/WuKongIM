package node

import (
	"bytes"
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

type ownerRoutesReaderFunc func(context.Context, presence.RouteTarget, []string) (presence.OwnerRouteSnapshot, error)

func (f ownerRoutesReaderFunc) ReadOwnerRoutes(ctx context.Context, t presence.RouteTarget, u []string) (presence.OwnerRouteSnapshot, error) {
	return f(ctx, t, u)
}

func TestOwnerRoutesRPCPreservesGenerationAndRejectsMalformedFrames(t *testing.T) {
	target := presence.RouteTarget{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}
	route := presence.Route{UID: "bob", OwnerNodeID: 2, OwnerBootID: 22, SessionID: 8, OwnerSeq: 1}
	adapter := New(Options{OwnerRoutes: ownerRoutesReaderFunc(func(_ context.Context, got presence.RouteTarget, uids []string) (presence.OwnerRouteSnapshot, error) {
		if got != target || len(uids) != 1 || uids[0] != "bob" {
			t.Fatalf("request target=%+v uids=%v", got, uids)
		}
		return presence.OwnerRouteSnapshot{OwnerNodeID: 2, OwnerBootID: 22, Routes: []presence.Route{route}}, nil
	})})
	request, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: presenceOpReadOwnerRoutes, EndpointGroups: []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"bob"}}}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.HandlePresenceOwnerRPC(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeOwnerRoutes(response)
	if err != nil || got.OwnerNodeID != 2 || got.OwnerBootID != 22 || len(got.Routes) != 1 || got.Routes[0] != route {
		t.Fatalf("response=%+v err=%v", got, err)
	}
	for i := 0; i < len(response); i++ {
		if _, err := decodeOwnerRoutes(response[:i]); err == nil {
			t.Fatalf("accepted truncation at %d", i)
		}
	}
	if _, err := decodeOwnerRoutes(append(bytes.Clone(response), 0)); err == nil {
		t.Fatal("accepted trailing bytes")
	}
	if _, err := adapter.HandlePresenceOwnerRPC(context.Background(), append(bytes.Clone(request), 0)); err == nil {
		t.Fatal("accepted trailing request bytes")
	}
}
