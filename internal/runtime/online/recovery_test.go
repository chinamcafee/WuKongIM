package online

import "testing"

func TestOwnerRecoveryIndexOnlyReturnsExactActiveSessions(t *testing.T) {
	r := NewRegistry(RegistryOptions{})
	a := LocalSession{Route: OwnerRoute{UID: "bob", SessionID: 1, OwnerNodeID: 2, OwnerBootID: 20}}
	if err := r.RegisterPending(a); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.ActiveRoutesByUIDs([]string{"bob"}, 1); !ok || len(got) != 0 {
		t.Fatalf("pending=%+v complete=%v", got, ok)
	}
	if err := r.MarkActive(1); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.ActiveRoutesByUIDs([]string{"bob"}, 1); !ok || len(got) != 1 || got[0].SessionID != 1 {
		t.Fatalf("active=%+v complete=%v", got, ok)
	}
	b := a
	b.Route.SessionID = 2
	if err := r.RegisterPending(b); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkActive(2); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.ActiveRoutesByUIDs([]string{"bob"}, 1); ok || len(got) != 0 {
		t.Fatalf("saturated response=%+v complete=%v", got, ok)
	}
	r.MarkClosingAndUnregister(1)
	b.Route.UID = "carol"
	if err := r.RegisterPending(b); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.ActiveRoutesByUIDs([]string{"bob", "carol"}, 2); !ok || len(got) != 0 {
		t.Fatalf("stale index=%+v complete=%v", got, ok)
	}
}
