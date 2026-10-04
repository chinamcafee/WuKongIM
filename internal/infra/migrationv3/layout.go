// Package migrationv3 adapts native v3 storage and bootstrap snapshots to the
// offline migration workflow. It never copies original v2 consensus state.
package migrationv3

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/controller/planner"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

type layout struct {
	state     state.ClusterState
	nodes     []uint64
	placement *channels.SlotPlacementResolver
}

// ValidatePlan checks target topology before the expensive stopped-source scan.
func ValidatePlan(ctx context.Context, plan migration.TargetPlan) error {
	_, err := newLayout(ctx, plan)
	return err
}

// newLayout uses the native placement planner for a fresh generation. Index 1
// is its initial snapshot, never a claim about an original v2 committed entry.
func newLayout(ctx context.Context, plan migration.TargetPlan) (*layout, error) {
	if plan.ClusterID == "" || strings.TrimSpace(plan.ClusterID) != plan.ClusterID || plan.CreatedAt.Unix() <= 0 || plan.SlotCount == 0 || plan.SlotCount > 256 || plan.HashSlotCount != 256 || plan.Replicas == 0 || plan.ChannelReplicas == 0 || len(plan.Nodes) == 0 || len(plan.Nodes) > 1024 || int(plan.Replicas) > len(plan.Nodes) || int(plan.ChannelReplicas) > len(plan.Nodes) {
		return nil, errors.New("invalid fresh target cluster plan")
	}
	l := &layout{state: state.ClusterState{SchemaVersion: state.CurrentSchemaVersion, ClusterID: plan.ClusterID, Revision: 1, AppliedRaftIndex: 1, UpdatedAt: plan.CreatedAt, Config: state.ClusterConfig{SlotCount: plan.SlotCount, HashSlotCount: 256, ReplicaCount: plan.Replicas, DefaultCapacityWeight: 1}}}
	seen := map[uint64]bool{}
	addrs := map[string]bool{}
	dirs := []string{}
	for _, n := range plan.Nodes {
		host, port, err := net.SplitHostPort(n.Addr)
		portNumber, portErr := strconv.Atoi(port)
		if err != nil || portErr != nil || host == "" || portNumber < 1 || portNumber > 65535 || n.NodeID == 0 || n.NodeID > 1023 || seen[n.NodeID] || addrs[n.Addr] || !filepath.IsAbs(n.DataDir) {
			return nil, errors.New("invalid or duplicate target node identity, endpoint or output directory")
		}
		dir := filepath.Clean(n.DataDir)
		for _, other := range dirs {
			if dir == other || strings.HasPrefix(dir, other+string(filepath.Separator)) || strings.HasPrefix(other, dir+string(filepath.Separator)) {
				return nil, errors.New("overlapping target directories")
			}
		}
		dirs = append(dirs, dir)
		seen[n.NodeID] = true
		addrs[n.Addr] = true
		l.nodes = append(l.nodes, n.NodeID)
		l.state.Nodes = append(l.state.Nodes, state.Node{NodeID: n.NodeID, Addr: n.Addr, Roles: []state.NodeRole{state.NodeRoleControllerVoter, state.NodeRoleData}, JoinState: state.NodeJoinStateActive, Status: state.NodeStatusAlive, CapacityWeight: 1})
		l.state.Controllers = append(l.state.Controllers, state.ControllerVoter{NodeID: n.NodeID, Addr: n.Addr, Role: state.ControllerRoleVoter})
	}
	slices.Sort(l.nodes)
	table, err := state.BuildInitialHashSlotTable(plan.SlotCount, 256)
	if err != nil {
		return nil, err
	}
	l.state.HashSlots = table
	p := planner.NewBootstrapPlanner()
	for range plan.SlotCount {
		d, err := p.Next(ctx, planner.View{State: l.state, Now: plan.CreatedAt})
		if err != nil {
			return nil, err
		}
		if d.Command.Assignment == nil {
			return nil, errors.New("target native placement planner did not produce an assignment")
		}
		l.state.Slots = append(l.state.Slots, *d.Command.Assignment)
	}
	l.state.Normalize()
	if err := l.state.Validate(); err != nil {
		return nil, err
	}
	l.placement = channels.NewSlotPlacementResolver(l, l, int(plan.ChannelReplicas))
	return l, nil
}

func (l *layout) RouteKey(key string) (routing.Route, error) {
	hash := routing.HashSlotForKey(key, 256)
	for _, span := range l.state.HashSlots.Ranges {
		if hash >= span.From && hash <= span.To {
			s := l.state.Slots[span.SlotID-1]
			return routing.Route{HashSlot: hash, SlotID: s.SlotID, Peers: append([]uint64(nil), s.DesiredPeers...), PreferredLeader: s.PreferredLeader, Revision: 1, ConfigEpoch: 1}, nil
		}
	}
	return routing.Route{}, errors.New("target hash slot missing")
}

func (l *layout) PlacementDataNodes(_ context.Context, revision uint64) ([]uint64, error) {
	if revision != 1 {
		return nil, errors.New("target placement generation mismatch")
	}
	return append([]uint64(nil), l.nodes...), nil
}
