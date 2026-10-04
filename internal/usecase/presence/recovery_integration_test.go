//go:build integration

package presence

import (
	"context"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"testing"
	"time"
)

func TestAuthorityRecoveryProvenLookupDoesNotWaitForColdLane(t *testing.T) {
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1}
	directory.BecomeAuthority(target)
	row := Route{UID: "hot", OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 3}
	if err := directory.InstallRecoveredRoutes(target, []string{"hot"}, []Route{row}); err != nil {
		t.Fatal(err)
	}
	recovery := NewAuthorityRecovery(directory, recoveryOwnersFunc(func(context.Context, RouteTarget, []string) ([]Route, error) {
		t.Error("hot proof queried owners")
		return nil, nil
	}))
	recovery.lanes[1] <- struct{}{}
	defer func() { <-recovery.lanes[1] }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rows, err := recovery.Endpoints(ctx, target, []string{"hot"})
	if err != nil || len(rows) != 1 || rows[0] != row {
		t.Fatalf("hot lookup blocked by cold lane: rows=%+v err=%v", rows, err)
	}
}
