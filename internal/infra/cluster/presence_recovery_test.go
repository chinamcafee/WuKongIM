package cluster

import (
	"context"
	"errors"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

type recoveryCluster struct {
	*fakePresenceCluster
	snapshot control.Snapshot
}

func (n *recoveryCluster) LocalControllerSnapshot(context.Context) (control.Snapshot, error) {
	return n.snapshot, nil
}

type recoveryReaderFunc func(context.Context, presence.RouteTarget, []string) (presence.OwnerRouteSnapshot, error)

func (f recoveryReaderFunc) ReadOwnerRoutes(ctx context.Context, t presence.RouteTarget, u []string) (presence.OwnerRouteSnapshot, error) {
	return f(ctx, t, u)
}

func TestRecoveryReadsSurvivingIngressAndRejectsStaleOwnerGeneration(t *testing.T) {
	target := presence.RouteTarget{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}
	node := &recoveryCluster{fakePresenceCluster: &fakePresenceCluster{nodeID: 1, route: cluster.Route{HashSlot: 101, SlotID: 5, Leader: 1, LeaderTerm: 4, ConfigEpoch: 1}}}
	for _, id := range []uint64{1, 2} {
		node.snapshot.Nodes = append(node.snapshot.Nodes, control.Node{NodeID: id, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive, Health: control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh, RuntimeReady: true}})
	}
	registry := online.NewRegistry(online.RegistryOptions{})
	route := online.OwnerRoute{UID: "bob", HashSlot: 101, OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 8}
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
	adapter := accessnode.New(accessnode.Options{OwnerRoutes: owner})
	node.rpcByNode = map[uint64]cluster.NodeRPCHandler{2: presenceOwnerRPCHandler{adapter: adapter}}
	local := recoveryReaderFunc(func(context.Context, presence.RouteTarget, []string) (presence.OwnerRouteSnapshot, error) {
		return presence.OwnerRouteSnapshot{OwnerNodeID: 1, OwnerBootID: 11}, nil
	})
	source := NewPresenceRecoveryOwners(node, local)
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	directory.BecomeAuthority(target)
	authorityAdapter := NewPresenceDirectoryAuthority(directory)
	authorityAdapter.SetRecovery(presence.NewAuthorityRecovery(directory, source))
	results := authorityAdapter.EndpointsByTargets(context.Background(), []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"bob", "offline"}}})
	if len(results) != 1 || results[0].Err != nil || len(results[0].Routes) != 1 || results[0].Routes[0].OwnerBootID != 22 {
		t.Fatalf("reconstructed=%+v", results)
	}
	// A response advertising the current boot may not smuggle a previous-boot route.
	forged := recoveryReaderFunc(func(context.Context, presence.RouteTarget, []string) (presence.OwnerRouteSnapshot, error) {
		return presence.OwnerRouteSnapshot{OwnerNodeID: 2, OwnerBootID: 23, Routes: results[0].Routes}, nil
	})
	node.rpcByNode[2] = presenceOwnerRPCHandler{adapter: accessnode.New(accessnode.Options{OwnerRoutes: forged})}
	if _, err := source.RecoverRoutes(context.Background(), target, []string{"bob"}); !errors.Is(err, authority.ErrStaleRoute) {
		t.Fatalf("old generation proof=%v", err)
	}
}

func TestRecoveryOwnerMembershipDoesNotAssumeDataReplicaRole(t *testing.T) {
	nodes := recoveryOwnerNodes(control.Snapshot{Nodes: []control.Node{
		{NodeID: 3, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateJoining},
		{NodeID: 2, Roles: []control.Role{control.RoleController}, JoinState: control.NodeJoinStateActive},
		{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
		{NodeID: 4, JoinState: control.NodeJoinStateRemoved},
	}})
	if len(nodes) != 2 || nodes[0].NodeID != 1 || nodes[1].NodeID != 2 {
		t.Fatalf("owner membership=%+v, want both active ingress-capable members", nodes)
	}
}

type recoveryBatchReaderFunc func(context.Context, []presence.EndpointLookupGroup) []presence.OwnerRouteResult

func (f recoveryBatchReaderFunc) ReadOwnerRoutesByTargets(ctx context.Context, g []presence.EndpointLookupGroup) []presence.OwnerRouteResult {
	return f(ctx, g)
}
func (f recoveryBatchReaderFunc) ReadOwnerRoutes(ctx context.Context, target presence.RouteTarget, uids []string) (presence.OwnerRouteSnapshot, error) {
	r := f(ctx, []presence.EndpointLookupGroup{{Target: target, UIDs: uids}})[0]
	return r.Snapshot, r.Err
}

func TestRecoveryChildDeadlineRemainsRetryableAndPreservesParentCancellation(t *testing.T) {
	target := presence.RouteTarget{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}
	node := &recoveryCluster{fakePresenceCluster: &fakePresenceCluster{nodeID: 1, route: cluster.Route{HashSlot: 101, SlotID: 5, Leader: 1, LeaderTerm: 4, ConfigEpoch: 1}}}
	node.snapshot.Nodes = []control.Node{{NodeID: 1, JoinState: control.NodeJoinStateActive}}
	groups := []presence.EndpointLookupGroup{{Target: target, UIDs: []string{"a"}}, {Target: target, UIDs: []string{"b"}}}
	calls := 0
	reader := recoveryBatchReaderFunc(func(context.Context, []presence.EndpointLookupGroup) []presence.OwnerRouteResult {
		calls++
		snapshot := presence.OwnerRouteSnapshot{OwnerNodeID: 1, OwnerBootID: 11}
		results := []presence.OwnerRouteResult{{Snapshot: snapshot}, {Snapshot: snapshot}}
		if calls == 1 {
			results[1] = presence.OwnerRouteResult{Err: context.DeadlineExceeded}
		}
		return results
	})
	source := NewPresenceRecoveryOwners(node, reader)
	results := source.RecoverRoutesByTargets(context.Background(), groups)
	if results[0].Err != nil || !errors.Is(results[1].Err, authority.ErrRouteNotReady) {
		t.Fatalf("child timeout escaped retry contract: %+v", results)
	}
	results = source.RecoverRoutesByTargets(context.Background(), groups)
	if results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("retry failed: %+v", results)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source = NewPresenceRecoveryOwners(node, recoveryBatchReaderFunc(func(context.Context, []presence.EndpointLookupGroup) []presence.OwnerRouteResult {
		cancel()
		return []presence.OwnerRouteResult{{Err: context.DeadlineExceeded}, {Err: authority.ErrNotLeader}}
	}))
	for _, result := range source.RecoverRoutesByTargets(ctx, groups) {
		if !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("parent cancellation mapped to %v", result.Err)
		}
	}
}
