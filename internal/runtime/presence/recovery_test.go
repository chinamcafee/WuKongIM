package presence

import (
	"errors"
	"fmt"
	"testing"
)

func TestAuthorityChangeCannotReportUnreconstructedUIDOffline(t *testing.T) {
	d := NewDirectory(DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	old := RouteTarget{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 3, ConfigEpoch: 1}
	d.BecomeAuthority(old)
	route := Route{UID: "bob", OwnerNodeID: 2, OwnerBootID: 22, SessionID: 8, OwnerSeq: 1}
	if _, err := d.RegisterRoute(old, route); err != nil {
		t.Fatal(err)
	}
	next := old
	next.LeaderTerm++
	d.BecomeAuthority(next)
	if routes, err := d.EndpointsByUID(next, "bob"); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("unreconstructed connected UID: routes=%+v error=%v, want route not ready", routes, err)
	}
	got := d.EndpointsByTargets([]EndpointLookupGroup{{Target: next, UIDs: []string{"bob"}}})
	if len(got) != 1 || !errors.Is(got[0].Err, ErrRouteNotReady) {
		t.Fatalf("group lookup=%+v", got)
	}
}

func TestRecoveryProofDistinguishesOfflineTombstonesAndStaleAuthority(t *testing.T) {
	d := NewDirectory(DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := RouteTarget{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}
	d.BecomeAuthority(target)
	route := Route{UID: "bob", OwnerNodeID: 2, OwnerBootID: 22, SessionID: 8, OwnerSeq: 1}
	if err := d.UnregisterRoute(target, route.Identity(), 2); err != nil {
		t.Fatal(err)
	}
	if err := d.InstallRecoveredRoutes(target, []string{"bob", "offline"}, []Route{route}); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"bob", "offline"} {
		if got, err := d.EndpointsByUID(target, uid); err != nil || len(got) != 0 {
			t.Fatalf("uid %s: got=%+v err=%v", uid, got, err)
		}
	}
	newer := route
	newer.OwnerBootID = 23
	newer.SessionID = 9
	newer.OwnerSeq = 3
	if _, err := d.RegisterRoute(target, newer); err != nil {
		t.Fatal(err)
	}
	if err := d.TouchRoutes(target, []Route{route}); err != nil {
		t.Fatal(err)
	}
	got, err := d.EndpointsByUID(target, "bob")
	if err != nil || len(got) != 1 || got[0] != newer {
		t.Fatalf("stale owner replaced current route: %+v err=%v", got, err)
	}
	next := target
	next.LeaderTerm++
	d.BecomeAuthority(next)
	if err := d.InstallRecoveredRoutes(target, []string{"bob"}, []Route{newer}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("stale proof=%v", err)
	}
	if _, err := d.EndpointsByUID(next, "bob"); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("new authority prematurely ready: %v", err)
	}
	if err := d.InstallRecoveredRoutes(next, []string{"bob"}, []Route{newer}); err != nil {
		t.Fatal(err)
	}
	got, err = d.EndpointsByUID(next, "bob")
	if err != nil || len(got) != 1 || got[0] != newer {
		t.Fatalf("recovered=%+v error=%v", got, err)
	}
}

func TestRecoveryProofCacheEvictionRequiresNewProof(t *testing.T) {
	d := NewDirectory(DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1}
	d.BecomeAuthority(target)
	for i := 0; i < recoveryCacheSize+1; i++ {
		if err := d.InstallRecoveredRoutes(target, []string{fmt.Sprint(i)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.EndpointsByUID(target, "0"); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("evicted proof became offline: %v", err)
	}
	if got, err := d.EndpointsByUID(target, fmt.Sprint(recoveryCacheSize)); err != nil || len(got) != 0 {
		t.Fatalf("latest offline proof=%+v err=%v", got, err)
	}
	shard := d.shard(target.HashSlot)
	if len(shard.slots[target.HashSlot].recovered) != recoveryCacheSize {
		t.Fatal("unbounded proof cache")
	}
}
