//go:build integration

package cluster

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

type recoveryMultiSlotCluster struct{ *recoveryCluster }

func (n *recoveryMultiSlotCluster) RouteHashSlot(h uint16) (cluster.Route, error) {
	return cluster.Route{HashSlot: h, SlotID: uint32(h % 12), Leader: 1, LeaderTerm: 4, ConfigEpoch: 1}, nil
}

type unavailableRecoveryOwner struct{ calls atomic.Int32 }

func (n *unavailableRecoveryOwner) HandleRPC(ctx context.Context, _ []byte) ([]byte, error) {
	n.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

// A default-sized delivery plan may span all 256 Hash Slots. One blackholed
// stale member must cost one bounded owner read, not one timeout per target.
func TestRecoveryColdFanoutFitsDeliveryDeadlineWithUnavailableOwner(t *testing.T) {
	node := &recoveryMultiSlotCluster{&recoveryCluster{fakePresenceCluster: &fakePresenceCluster{nodeID: 1}}}
	for _, id := range []uint64{1, 2, 3} {
		health := control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh}
		if id == 3 {
			health.Status = control.NodeDown
			health.Freshness = control.NodeHealthStale
		}
		node.snapshot.Nodes = append(node.snapshot.Nodes, control.Node{NodeID: id, JoinState: control.NodeJoinStateActive, Health: health})
	}
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	registry := online.NewRegistry(online.RegistryOptions{})
	groups := make([]presence.EndpointLookupGroup, 256)
	for i := range groups {
		route, _ := node.RouteHashSlot(uint16(i))
		target := routeTargetFromClusterRoute(route)
		directory.BecomeAuthority(target)
		groups[i].Target = target
		for j := range 2 {
			uid := fmt.Sprintf("fanout-%d-%d", i, j)
			groups[i].UIDs = append(groups[i].UIDs, uid)
			session := uint64(i*2 + j + 1)
			if err := registry.RegisterPending(online.LocalSession{Route: online.OwnerRoute{UID: uid, HashSlot: uint16(i), OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: session}}); err != nil {
				t.Fatal(err)
			}
			if err := registry.MarkActive(session); err != nil {
				t.Fatal(err)
			}
		}
	}
	owner := presence.NewOwnerRecovery(registry, 2, 22, func(target presence.RouteTarget) error { return ValidatePresenceTarget(node, target) })
	unavailable := &unavailableRecoveryOwner{}
	node.rpcByNode = map[uint64]cluster.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: owner})}, 3: unavailable}
	local := presence.NewOwnerRecovery(online.NewRegistry(online.RegistryOptions{}), 1, 11, func(target presence.RouteTarget) error { return ValidatePresenceTarget(node, target) })
	adapter := NewPresenceDirectoryAuthority(directory)
	adapter.SetRecovery(presence.NewAuthorityRecovery(directory, NewPresenceRecoveryOwners(node, local)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := adapter.EndpointsByTargets(ctx, groups)
	for i, result := range results {
		if result.Err != nil || len(result.Routes) != 2 {
			t.Fatalf("group %d: routes=%d err=%v; unavailable owner reads=%d", i, len(result.Routes), result.Err, unavailable.calls.Load())
		}
		for j, route := range result.Routes {
			if route.UID != groups[i].UIDs[j] || route.OwnerNodeID != 2 {
				t.Fatalf("misaligned group %d: %+v", i, result.Routes)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("delivery deadline consumed: %v", err)
	}
	if got := unavailable.calls.Load(); got != 1 {
		t.Fatalf("unavailable owner reads=%d, want 1 per 512-UID page", got)
	}
}
