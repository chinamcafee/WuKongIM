package presence

import (
	"context"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// OwnerRouteSnapshot binds an active-route read, including an empty read, to
// the answering process generation. Concrete sessions never leave the owner.
type OwnerRouteSnapshot struct {
	// OwnerNodeID is the answering gateway owner.
	OwnerNodeID uint64
	// OwnerBootID is the process generation that owns every returned route.
	OwnerBootID uint64
	// Routes contains only active projections; an empty complete snapshot is valid.
	Routes []Route
}

// OwnerRouteReader reads a bounded exact-target page from one gateway owner.
type OwnerRouteReader interface {
	ReadOwnerRoutes(context.Context, RouteTarget, []string) (OwnerRouteSnapshot, error)
}

// ActiveRouteRegistry reads owner-indexed pages without exposing concrete sessions.
type ActiveRouteRegistry interface {
	ActiveRoutesByUIDs([]string, int) ([]OwnerRoute, bool)
}

// OwnerRecovery exposes active owner-local projections after checking the
// caller's authority against the owner's current route publication.
type OwnerRecovery struct {
	local          ActiveRouteRegistry
	nodeID, bootID uint64
	validate       func(RouteTarget) error
}

// NewOwnerRecovery binds an owner registry and its process identity to route validation.
func NewOwnerRecovery(local ActiveRouteRegistry, nodeID, bootID uint64, validate func(RouteTarget) error) *OwnerRecovery {
	return &OwnerRecovery{local: local, nodeID: nodeID, bootID: bootID, validate: validate}
}

// ReadOwnerRoutes rechecks the current authority around a bounded active-route read.
func (r *OwnerRecovery) ReadOwnerRoutes(ctx context.Context, target RouteTarget, uids []string) (OwnerRouteSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return OwnerRouteSnapshot{}, err
	}
	if r == nil || r.local == nil || r.validate == nil || r.nodeID == 0 || r.bootID == 0 || len(uids) > authority.RecoveryBatchSize {
		return OwnerRouteSnapshot{}, authority.ErrRouteNotReady
	}
	if err := r.validate(target); err != nil {
		return OwnerRouteSnapshot{}, err
	}
	rows, complete := r.local.ActiveRoutesByUIDs(uids, 4096)
	if !complete {
		return OwnerRouteSnapshot{}, authority.ErrRouteNotReady
	}
	result := OwnerRouteSnapshot{OwnerNodeID: r.nodeID, OwnerBootID: r.bootID, Routes: make([]Route, 0, len(rows))}
	for _, row := range rows {
		if row.OwnerNodeID != r.nodeID || row.OwnerBootID != r.bootID || row.HashSlot != target.HashSlot {
			return OwnerRouteSnapshot{}, authority.ErrStaleRoute
		}
		result.Routes = append(result.Routes, routeFromOwnerRoute(row))
	}
	if err := r.validate(target); err != nil {
		return OwnerRouteSnapshot{}, err
	}
	return result, nil
}

// OwnerRouteResult preserves an exact target's snapshot or rejection independently
// of its siblings in a bounded owner read.
type OwnerRouteResult struct {
	Snapshot OwnerRouteSnapshot
	Err      error
}

// OwnerRouteBatchReader coalesces one page across Hash Slots without repeating
// the network timeout for each target.
type OwnerRouteBatchReader interface {
	ReadOwnerRoutesByTargets(context.Context, []EndpointLookupGroup) []OwnerRouteResult
}

// ValidOwnerRecoveryPage bounds both request work and the default delivery page.
func ValidOwnerRecoveryPage(groups []EndpointLookupGroup) bool {
	if len(groups) == 0 || len(groups) > 256 {
		return false
	}
	total := 0
	for _, group := range groups {
		total += len(group.UIDs)
		if total > authority.RecoveryBatchSize {
			return false
		}
	}
	return true
}

// ReadOwnerRoutesByTargets preserves each target's fence and the owner boot
// identity while limiting the entire response to 4,096 active route rows.
func (r *OwnerRecovery) ReadOwnerRoutesByTargets(ctx context.Context, groups []EndpointLookupGroup) []OwnerRouteResult {
	results := make([]OwnerRouteResult, len(groups))
	if !ValidOwnerRecoveryPage(groups) || r == nil || r.validate == nil {
		for i := range results {
			results[i].Err = authority.ErrRouteNotReady
		}
		return results
	}
	total := 0
	for i, group := range groups {
		results[i].Snapshot, results[i].Err = r.ReadOwnerRoutes(ctx, group.Target, group.UIDs)
		total += len(results[i].Snapshot.Routes)
		if total > 4096 {
			results[i] = OwnerRouteResult{Err: authority.ErrRouteNotReady}
		}
	}
	// A later target read may overlap a Slot movement. Revalidate earlier groups
	// too, retaining valid siblings instead of failing the whole page.
	for i, group := range groups {
		if results[i].Err == nil {
			if err := r.validate(group.Target); err != nil {
				results[i] = OwnerRouteResult{Err: err}
			}
		}
	}
	return results
}
