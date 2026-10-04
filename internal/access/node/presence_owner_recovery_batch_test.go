package node

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestOwnerRoutesBatchRPCBoundsFencesAndSingletonCompatibility(t *testing.T) {
	target := presence.RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 4}
	stale := target
	stale.LeaderTerm--
	registry := online.NewRegistry(online.RegistryOptions{})
	route := online.OwnerRoute{UID: "bob", HashSlot: 1, OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 8}
	if err := registry.RegisterPending(online.LocalSession{Route: route}); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkActive(8); err != nil {
		t.Fatal(err)
	}
	owner := presence.NewOwnerRecovery(registry, 2, 22, func(got presence.RouteTarget) error {
		if got != target {
			return authority.ErrNotLeader
		}
		return nil
	})
	adapter := New(Options{OwnerRoutes: owner})
	groups := []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"bob"}}, {Target: stale, UIDs: []string{"bob"}}}
	request, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: presenceOpReadOwnerRoutesByTargets, EndpointGroups: groups})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := adapter.HandlePresenceOwnerRPC(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	results, err := decodeOwnerRoutesByTargets(reply)
	if err != nil || len(results) != 2 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	if results[0].Err != nil || results[0].Snapshot.OwnerBootID != 22 || len(results[0].Snapshot.Routes) != 1 || results[0].Snapshot.Routes[0].UID != "bob" || !errors.Is(results[1].Err, authority.ErrNotLeader) {
		t.Fatalf("aligned results=%+v", results)
	}
	for i := range len(reply) {
		if _, err := decodeOwnerRoutesByTargets(reply[:i]); err == nil {
			t.Fatalf("accepted truncated batch at %d", i)
		}
	}
	if _, err := decodeOwnerRoutesByTargets(append(bytes.Clone(reply), 0)); err == nil {
		t.Fatal("accepted trailing batch bytes")
	}
	if _, err := adapter.HandlePresenceOwnerRPC(context.Background(), append(bytes.Clone(request), 0)); err == nil {
		t.Fatal("accepted trailing request bytes")
	}
	if _, err := adapter.handleOwnerRoutesByTargets(context.Background(), presenceRPCRequest{EndpointGroups: []presence.EndpointLookupGroup{{Target: target, UIDs: make([]string, 513)}}}); err == nil {
		t.Fatal("accepted oversized owner page")
	}
	if presenceOpAdvanceCredentialFenceID != 9 || presenceOpAckCredentialActionID != 10 || presenceOpReadOwnerRoutesID != 11 || presenceOpReadOwnerRoutesByTargetsID != 12 {
		t.Fatal("changed existing owner operation ID")
	}
	singleton, err := adapter.handleOwnerRoutes(context.Background(), presenceRPCRequest{EndpointGroups: groups[:1]})
	if err != nil {
		t.Fatal(err)
	}
	// Fixed v1 framing remains independently readable after introducing v2 batches.
	want := append([]byte(nil), []byte{'W', 'K', 'V', 'O', 1}...)
	want = appendString(want, presenceRPCStatusForError(nil))
	want = appendUvarint(want, 2)
	want = appendUvarint(want, 22)
	want = appendPresenceRoutes(want, results[0].Snapshot.Routes)
	if !bytes.Equal(singleton, want) {
		t.Fatal("singleton response bytes changed")
	}
	if _, err := decodeOwnerRoutes(reply); err == nil {
		t.Fatal("v1 decoder accepted v2 framing")
	}
}
