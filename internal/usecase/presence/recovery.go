package presence

import (
	"context"
	"errors"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// RecoveryDirectory owns reconstructed UID proofs under exact authority fences.
type RecoveryDirectory interface {
	RecoveryUIDs(RouteTarget, []string) ([]string, error)
	InstallRecoveredRoutes(RouteTarget, []string, []Route) error
	EndpointsByUIDs(RouteTarget, []string) ([]Route, error)
}

// RecoveryOwners returns a complete bounded read from all current gateway owners.
// Partial/unavailable owner evidence must be returned as an error.
type RecoveryOwners interface {
	RecoverRoutes(context.Context, RouteTarget, []string) ([]Route, error)
}

// AuthorityRecovery reconstructs requested UIDs with bounded owner queries.
// Fixed admission lanes coalesce overlapping lookups without per-UID goroutines
// or an unbounded in-flight map. Directory ownership fences every completion.
type AuthorityRecovery struct {
	directory RecoveryDirectory
	owners    RecoveryOwners
	lanes     [32]chan struct{}
}

// NewAuthorityRecovery allocates fixed admission lanes for owner reconstruction.
func NewAuthorityRecovery(directory RecoveryDirectory, owners RecoveryOwners) *AuthorityRecovery {
	r := &AuthorityRecovery{directory: directory, owners: owners}
	for i := range r.lanes {
		r.lanes[i] = make(chan struct{}, 1)
	}
	return r
}

// RecoveryBatchOwners returns aligned proofs while sharing each owner read
// across every target in one bounded recipient page.
type RecoveryBatchOwners interface {
	RecoverRoutesByTargets(context.Context, []EndpointLookupGroup) []EndpointLookupResult
}

// Endpoints resolves a single target through the same bounded batch path.
func (r *AuthorityRecovery) Endpoints(ctx context.Context, target RouteTarget, uids []string) ([]Route, error) {
	result := r.EndpointsByTargets(ctx, []EndpointLookupGroup{{Target: target, UIDs: uids}})[0]
	return result.Routes, result.Err
}

// EndpointsByTargets packs at most 512 UIDs and 256 targets per owner round.
// A cold page shares the unavailable-owner timeout across all its Hash Slots.
// Proven lookups stay outside recovery admission; failed groups remain aligned.
func (r *AuthorityRecovery) EndpointsByTargets(ctx context.Context, groups []EndpointLookupGroup) []EndpointLookupResult {
	results := make([]EndpointLookupResult, len(groups))
	page := make([]EndpointLookupGroup, 0, min(len(groups), 256))
	indexes := make([]int, 0, cap(page))
	count := 0
	flush := func() {
		got := r.recoverPage(ctx, page)
		for j, result := range got {
			i := indexes[j]
			if results[i].Err != nil {
				continue
			}
			if result.Err != nil {
				results[i] = result
			} else {
				results[i].Routes = append(results[i].Routes, result.Routes...)
			}
		}
		page = page[:0]
		indexes = indexes[:0]
		count = 0
	}
	for i, group := range groups {
		for start := 0; start < len(group.UIDs) || start == 0; {
			if count == authority.RecoveryBatchSize || len(page) == 256 {
				flush()
			}
			end := min(start+authority.RecoveryBatchSize-count, len(group.UIDs))
			page = append(page, EndpointLookupGroup{Target: group.Target, UIDs: group.UIDs[start:end]})
			indexes = append(indexes, i)
			count += end - start
			if end == len(group.UIDs) {
				break
			}
			start = end
		}
	}
	if len(page) != 0 {
		flush()
	}
	return results
}

// recoverPage locks distinct cold lanes in a fixed order and rechecks proofs
// after admission, coalescing concurrent reconstructions without lock cycles.
func (r *AuthorityRecovery) recoverPage(ctx context.Context, groups []EndpointLookupGroup) []EndpointLookupResult {
	results := make([]EndpointLookupResult, len(groups))
	fail := func(err error) []EndpointLookupResult {
		for i := range results {
			results[i] = EndpointLookupResult{Err: err}
		}
		return results
	}
	if r == nil || r.directory == nil || r.owners == nil {
		return fail(authority.ErrRouteNotReady)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	var lanes [32]bool
	for i, group := range groups {
		results[i].Routes, results[i].Err = r.directory.EndpointsByUIDs(group.Target, group.UIDs)
		if errors.Is(results[i].Err, authority.ErrRouteNotReady) {
			lanes[int(group.Target.HashSlot)%len(r.lanes)] = true
		}
	}
	locked := make([]int, 0, len(lanes))
	defer func() {
		for _, i := range locked {
			<-r.lanes[i]
		}
	}()
	for i, needed := range lanes {
		if !needed {
			continue
		}
		select {
		case r.lanes[i] <- struct{}{}:
			locked = append(locked, i)
		case <-ctx.Done():
			for j := range results {
				if errors.Is(results[j].Err, authority.ErrRouteNotReady) {
					results[j].Err = ctx.Err()
				}
			}
			return results
		}
	}
	missing := make([]EndpointLookupGroup, 0, len(groups))
	indexes := make([]int, 0, len(groups))
	for i, group := range groups {
		if !errors.Is(results[i].Err, authority.ErrRouteNotReady) {
			continue
		}
		uids, err := r.directory.RecoveryUIDs(group.Target, group.UIDs)
		if err != nil {
			results[i].Err = err
			continue
		}
		if len(uids) == 0 {
			results[i].Routes, results[i].Err = r.directory.EndpointsByUIDs(group.Target, group.UIDs)
			continue
		}
		missing = append(missing, EndpointLookupGroup{Target: group.Target, UIDs: uids})
		indexes = append(indexes, i)
	}
	if len(missing) == 0 {
		return results
	}
	recovered := make([]EndpointLookupResult, len(missing))
	if batch, ok := r.owners.(RecoveryBatchOwners); ok {
		recovered = batch.RecoverRoutesByTargets(ctx, missing)
	} else {
		// Limited in-process compatibility implementations retain the single-target
		// port. Production uses the batch port and never retries unsupported RPCs serially.
		for i, group := range missing {
			recovered[i].Routes, recovered[i].Err = r.owners.RecoverRoutes(ctx, group.Target, group.UIDs)
		}
	}
	for j, i := range indexes {
		if len(recovered) != len(missing) {
			results[i].Err = authority.ErrRouteNotReady
			continue
		}
		if recovered[j].Err != nil {
			results[i].Err = recovered[j].Err
			continue
		}
		if err := r.directory.InstallRecoveredRoutes(missing[j].Target, missing[j].UIDs, recovered[j].Routes); err != nil {
			results[i].Err = err
			continue
		}
		results[i].Routes, results[i].Err = r.directory.EndpointsByUIDs(groups[i].Target, groups[i].UIDs)
	}
	return results
}
