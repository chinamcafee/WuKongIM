package presence

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultShardCount = 32
	deviceLevelSlave  = 0
	deviceLevelMaster = 1
)

// Directory stores authoritative presence routes for locally led hash slots.
type Directory struct {
	// localNodeID optionally verifies that incoming RouteTarget values point here.
	localNodeID      uint64
	credentialFences CredentialFenceLoader
	// shards spreads hash-slot authority state across independent locks.
	shards []directoryShard
	// touchRoutesTotal counts route touch entries accepted by the target fence.
	touchRoutesTotal atomic.Uint64
	// expiredRoutesTotal counts routes removed by TTL expiry.
	expiredRoutesTotal atomic.Uint64
}

type directoryShard struct {
	// mu protects all authority slots assigned to this shard.
	mu sync.RWMutex
	// slots holds per-hash-slot authority identities installed on this node.
	slots map[uint16]*authoritySlot
}

type authoritySlot struct {
	// target is the exact fencing token accepted by this authority identity.
	target RouteTarget
	// active contains committed routes by exact owner route identity.
	active map[identityKey]Route
	// byUID indexes active route identities for UID endpoint lookups.
	byUID map[string]map[identityKey]struct{}
	// pending contains conflict candidates waiting for action commit or abort.
	pending map[PendingRouteToken]pendingRoute
	// ownerSeq stores the latest owner sequence seen for each route identity.
	ownerSeq map[identityKey]uint64
	// tombstoneSeq stores explicit unregister fences for exact route identities.
	tombstoneSeq map[identityKey]uint64
	// expiryHeap orders non-empty activity-second buckets by oldest activity.
	expiryHeap expiryBucketHeap
	// expiryBySeen locates the unique bucket for each indexed activity second.
	expiryBySeen map[int64]*expiryBucket
	// expiryByKey locates the exact bucket membership for each indexed route.
	expiryByKey map[identityKey]*expiryBucket
	// nextID allocates shard-local pending route tokens.
	nextID uint64
	// credentialFences stores current admission state by UID/device flag.
	credentialFences map[credentialFenceKey]CredentialFence
	// credentialActions retains owner reconciliation work until the authority
	// receives an explicit acknowledgement. Equal-version retries therefore
	// cannot incorrectly report completion after a transient owner failure.
	credentialActions map[credentialActionKey]credentialAction
}

type pendingRoute struct {
	// route is the candidate that will become active on commit.
	route Route
	// conflicts are active route identities acknowledged by the caller.
	conflicts []identityKey
}

type identityKey struct {
	// uid is part of the exact route identity carried by authority RPCs.
	uid string
	// ownerNodeID is the gateway node that owns the route identity.
	ownerNodeID uint64
	// ownerBootID identifies the owner's process generation.
	ownerBootID uint64
	// sessionID is unique within one owner boot generation.
	sessionID uint64
}

type credentialFenceKey struct {
	uid  string
	flag uint8
}

type credentialActionKey struct {
	uid                       string
	ownerNodeID               uint64
	ownerBootID               uint64
	sessionID                 uint64
	expectedOwnerSeq          uint64
	expectedCredentialVersion uint64
	expectedLoginSessionID    string
}

type credentialAction struct {
	deviceFlag uint8
	action     RouteAction
}

// NewDirectory creates a sharded in-memory authority directory.
func NewDirectory(opts DirectoryOptions) *Directory {
	shardCount := opts.ShardCount
	if shardCount <= 0 {
		shardCount = defaultShardCount
	}
	d := &Directory{
		localNodeID:      opts.LocalNodeID,
		credentialFences: opts.CredentialFences,
		shards:           make([]directoryShard, shardCount),
	}
	for i := range d.shards {
		d.shards[i].slots = make(map[uint16]*authoritySlot)
	}
	return d
}

// SetCredentialFenceLoader installs the durable admission reader before the directory serves traffic.
func (d *Directory) SetCredentialFenceLoader(loader CredentialFenceLoader) {
	if d == nil {
		return
	}
	d.credentialFences = loader
}

// BecomeAuthority installs a fresh authority identity for one hash slot.
func (d *Directory) BecomeAuthority(target RouteTarget) {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	current := shard.slots[target.HashSlot]
	if current != nil {
		if sameAuthorityIdentity(current.target, target) {
			if target.RouteRevision >= current.target.RouteRevision {
				current.target = target
			}
			return
		}
	}
	shard.slots[target.HashSlot] = newAuthoritySlot(target)
}

// LoseAuthority clears authority state for one hash slot.
func (d *Directory) LoseAuthority(hashSlot uint16) {
	shard := d.shard(hashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	delete(shard.slots, hashSlot)
}

// RegisterRoute registers a route or stores it as pending when conflicts exist.
func (d *Directory) RegisterRoute(target RouteTarget, route Route) (RegisterResult, error) {
	return d.RegisterRouteContext(context.Background(), target, route)
}

// RegisterRouteContext validates durable credential state before entering the authority critical section.
func (d *Directory) RegisterRouteContext(ctx context.Context, target RouteTarget, route Route) (RegisterResult, error) {
	var loaded *CredentialFence
	if d != nil && d.credentialFences != nil {
		fence, err := d.credentialFences.LoadCredentialFence(ctx, route.UID, route.DeviceFlag)
		if err != nil {
			return RegisterResult{}, ErrRouteNotReady
		}
		loaded = &fence
	}
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return RegisterResult{}, err
	}
	return slot.registerLocked(route, loaded, time.Now().UnixMilli())
}

// CommitRoute promotes a pending conflict candidate and removes acknowledged conflicts.
func (d *Directory) CommitRoute(target RouteTarget, token PendingRouteToken) error {
	return d.CommitRouteContext(context.Background(), target, token)
}

// CommitRouteContext revalidates the pending route against durable credential
// metadata before it can become active. The load happens outside the shard lock.
func (d *Directory) CommitRouteContext(ctx context.Context, target RouteTarget, token PendingRouteToken) error {
	var loaded *CredentialFence
	if d != nil && d.credentialFences != nil {
		shard := d.shard(target.HashSlot)
		shard.mu.RLock()
		slot, err := d.validateTargetLocked(shard, target)
		if err != nil {
			shard.mu.RUnlock()
			return err
		}
		pending, ok := slot.pending[token]
		shard.mu.RUnlock()
		if !ok {
			return ErrRouteNotReady
		}
		fence, err := d.credentialFences.LoadCredentialFence(ctx, pending.route.UID, pending.route.DeviceFlag)
		if err != nil {
			return ErrRouteNotReady
		}
		loaded = &fence
	}
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	if loaded != nil {
		if err := slot.mergeLoadedFenceLocked(*loaded); err != nil {
			delete(slot.pending, token)
			return err
		}
	}
	return slot.commitRouteLocked(token, time.Now().UnixMilli())
}

// AbortRoute drops a pending conflict candidate without touching active routes.
func (d *Directory) AbortRoute(target RouteTarget, token PendingRouteToken) error {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	if _, ok := slot.pending[token]; !ok {
		return ErrRouteNotReady
	}
	delete(slot.pending, token)
	return nil
}

// UnregisterRoute tombstones and removes one exact route identity.
func (d *Directory) UnregisterRoute(target RouteTarget, identity RouteIdentity, ownerSeq uint64) error {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	key := makeIdentityKey(identity)
	if ownerSeq > slot.tombstoneSeq[key] {
		slot.tombstoneSeq[key] = ownerSeq
	}
	if ownerSeq > slot.ownerSeq[key] {
		slot.ownerSeq[key] = ownerSeq
	}
	if existing, ok := slot.active[key]; ok && existing.OwnerSeq <= ownerSeq {
		slot.removeActiveLocked(key, existing)
	}
	for token, pending := range slot.pending {
		if makeRouteIdentityKey(pending.route) == key && pending.route.OwnerSeq <= ownerSeq {
			delete(slot.pending, token)
		}
	}
	return nil
}

// TouchRoutes refreshes active owner activity and recreates non-conflicting missing routes.
func (d *Directory) TouchRoutes(target RouteTarget, routes []Route) error {
	return d.TouchRoutesContext(context.Background(), target, routes)
}

// TouchRoutesContext reloads every distinct durable UID/device fence before
// refreshing or recreating routes, so heartbeats cannot revive stale sessions.
func (d *Directory) TouchRoutesContext(ctx context.Context, target RouteTarget, routes []Route) error {
	loaded := make(map[credentialFenceKey]CredentialFence)
	if d != nil && d.credentialFences != nil {
		for _, route := range routes {
			key := credentialFenceKey{uid: route.UID, flag: route.DeviceFlag}
			if _, ok := loaded[key]; ok {
				continue
			}
			fence, err := d.credentialFences.LoadCredentialFence(ctx, route.UID, route.DeviceFlag)
			if err != nil {
				return ErrRouteNotReady
			}
			loaded[key] = fence
		}
	}
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	for _, fence := range loaded {
		if err := slot.mergeLoadedFenceLocked(fence); err != nil {
			return err
		}
	}
	for _, route := range routes {
		slot.touchLocked(route, time.Now().UnixMilli())
	}
	d.touchRoutesTotal.Add(uint64(len(routes)))
	return nil
}

// ExpireRoutesDetailed removes due active routes and reports bounded index diagnostics.
func (d *Directory) ExpireRoutesDetailed(now time.Time, ttl time.Duration) ExpireResult {
	result := ExpireResult{}
	for i := range d.shards {
		shard := &d.shards[i]
		shard.mu.Lock()
		for _, slot := range shard.slots {
			slotResult := slot.expireLocked(now, ttl)
			result.Expired += slotResult.Expired
			result.DueBuckets += slotResult.DueBuckets
			result.Examined += slotResult.Examined
			result.IndexRoutes += slotResult.IndexRoutes
			result.IndexBuckets += slotResult.IndexBuckets
		}
		shard.mu.Unlock()
	}
	if result.Expired > 0 {
		d.expiredRoutesTotal.Add(uint64(result.Expired))
	}
	return result
}

// ExpireRoutes removes active routes whose last observed activity is older than ttl.
func (d *Directory) ExpireRoutes(now time.Time, ttl time.Duration) int {
	return d.ExpireRoutesDetailed(now, ttl).Expired
}

// Snapshot returns aggregate authority route counts for bench diagnostics.
func (d *Directory) Snapshot() Snapshot {
	if d == nil {
		return Snapshot{ByHashSlot: map[uint16]int{}}
	}
	snap := Snapshot{
		ByHashSlot:         make(map[uint16]int),
		TouchRoutesTotal:   d.touchRoutesTotal.Load(),
		ExpiredRoutesTotal: d.expiredRoutesTotal.Load(),
	}
	for i := range d.shards {
		shard := &d.shards[i]
		shard.mu.RLock()
		for hashSlot, slot := range shard.slots {
			count := len(slot.active)
			if count > 0 {
				snap.ByHashSlot[hashSlot] = count
				snap.Active += count
			}
			snap.ExpiryIndexRoutes += len(slot.expiryByKey)
			snap.ExpiryIndexBuckets += len(slot.expiryHeap)
		}
		shard.mu.RUnlock()
	}
	return snap
}

// EndpointsByUID returns active authoritative routes for one UID.
func (d *Directory) EndpointsByUID(target RouteTarget, uid string) ([]Route, error) {
	shard := d.shard(target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return nil, err
	}
	return slot.endpointsByUIDLocked(uid), nil
}

// EndpointsByUIDs returns active routes for one exact authority target in UID input order.
func (d *Directory) EndpointsByUIDs(target RouteTarget, uids []string) ([]Route, error) {
	shard := d.shard(target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return nil, err
	}
	var routes []Route
	for _, uid := range uids {
		routes = append(routes, slot.endpointsByUIDLocked(uid)...)
	}
	return routes, nil
}

// EndpointsByTargets resolves exact-target groups while acquiring each directory shard once.
func (d *Directory) EndpointsByTargets(groups []EndpointLookupGroup) []EndpointLookupResult {
	results := make([]EndpointLookupResult, len(groups))
	if len(groups) == 0 {
		return results
	}
	if d == nil || len(d.shards) == 0 {
		for i := range results {
			results[i].Err = ErrNotLeader
		}
		return results
	}

	shardCount := len(d.shards)
	scratch := make([]int, shardCount*3+1)
	counts := scratch[:shardCount]
	offsets := scratch[shardCount : shardCount*2+1]
	next := scratch[shardCount*2+1:]
	for _, group := range groups {
		counts[int(group.Target.HashSlot)%shardCount]++
	}
	for shardIndex, count := range counts {
		offsets[shardIndex+1] = offsets[shardIndex] + count
	}
	copy(next, offsets[:shardCount])
	order := make([]int, len(groups))
	for groupIndex, group := range groups {
		shardIndex := int(group.Target.HashSlot) % shardCount
		order[next[shardIndex]] = groupIndex
		next[shardIndex]++
	}

	for shardIndex, count := range counts {
		if count == 0 {
			continue
		}
		shard := &d.shards[shardIndex]
		shard.mu.RLock()
		groupIndexes := order[offsets[shardIndex]:offsets[shardIndex+1]]
		routeCount := 0
		for _, groupIndex := range groupIndexes {
			group := groups[groupIndex]
			slot, err := d.validateTargetLocked(shard, group.Target)
			if err != nil {
				results[groupIndex].Err = err
				continue
			}
			for _, uid := range group.UIDs {
				routeCount += len(slot.byUID[uid])
			}
		}

		shardRoutes := make([]Route, routeCount)
		writeIndex := 0
		for _, groupIndex := range groupIndexes {
			if results[groupIndex].Err != nil {
				continue
			}
			group := groups[groupIndex]
			slot := shard.slots[group.Target.HashSlot]
			groupStart := writeIndex
			for _, uid := range group.UIDs {
				uidStart := writeIndex
				nowUnixMS := time.Now().UnixMilli()
				for key := range slot.byUID[uid] {
					if route, ok := slot.active[key]; ok && slot.routeAdmittedLocked(route, nowUnixMS) {
						shardRoutes[writeIndex] = route
						writeIndex++
					}
				}
				sortRoutes(shardRoutes[uidStart:writeIndex])
			}
			if writeIndex > groupStart {
				results[groupIndex].Routes = shardRoutes[groupStart:writeIndex:writeIndex]
			}
		}
		shard.mu.RUnlock()
	}
	return results
}

func (s *authoritySlot) endpointsByUIDLocked(uid string) []Route {
	keys := s.byUID[uid]
	if len(keys) == 0 {
		return nil
	}
	routes := make([]Route, 0, len(keys))
	nowUnixMS := time.Now().UnixMilli()
	for key := range keys {
		if route, ok := s.active[key]; ok && s.routeAdmittedLocked(route, nowUnixMS) {
			routes = append(routes, route)
		}
	}
	sortRoutes(routes)
	return routes
}

// Identity returns the immutable identity fields for this route.
func (r Route) Identity() RouteIdentity {
	return RouteIdentity{
		UID:         r.UID,
		OwnerNodeID: r.OwnerNodeID,
		OwnerBootID: r.OwnerBootID,
		SessionID:   r.SessionID,
	}
}

func (d *Directory) validateTarget(target RouteTarget) error {
	shard := d.shard(target.HashSlot)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	_, err := d.validateTargetLocked(shard, target)
	return err
}

func (d *Directory) validateTargetLocked(shard *directoryShard, target RouteTarget) (*authoritySlot, error) {
	if d.localNodeID != 0 && target.LeaderNodeID != d.localNodeID {
		return nil, ErrNotLeader
	}
	slot := shard.slots[target.HashSlot]
	if slot == nil || !sameAuthorityIdentity(slot.target, target) {
		return nil, ErrNotLeader
	}
	return slot, nil
}

func (d *Directory) shard(hashSlot uint16) *directoryShard {
	return &d.shards[int(hashSlot)%len(d.shards)]
}

func newAuthoritySlot(target RouteTarget) *authoritySlot {
	return &authoritySlot{
		target:            target,
		active:            make(map[identityKey]Route),
		byUID:             make(map[string]map[identityKey]struct{}),
		pending:           make(map[PendingRouteToken]pendingRoute),
		credentialFences:  make(map[credentialFenceKey]CredentialFence),
		credentialActions: make(map[credentialActionKey]credentialAction),
		ownerSeq:          make(map[identityKey]uint64),
		tombstoneSeq:      make(map[identityKey]uint64),
		expiryHeap:        make(expiryBucketHeap, 0),
		expiryBySeen:      make(map[int64]*expiryBucket),
		expiryByKey:       make(map[identityKey]*expiryBucket),
	}
}

func (s *authoritySlot) registerLocked(route Route, loaded *CredentialFence, nowUnixMS int64) (RegisterResult, error) {
	if loaded != nil {
		if err := s.mergeLoadedFenceLocked(*loaded); err != nil {
			return RegisterResult{}, err
		}
	} else if route.CredentialVersion > 0 {
		_ = s.mergeLoadedFenceLocked(CredentialFence{
			UID: route.UID, DeviceFlag: route.DeviceFlag, CredentialVersion: route.CredentialVersion,
			LoginSessionID: route.LoginSessionID, Status: CredentialStatusActive, ExpiresAtUnixMS: route.ExpiresAtUnixMS,
		})
	}
	if !s.routeAdmittedLocked(route, nowUnixMS) {
		return RegisterResult{}, ErrStaleRoute
	}
	key := makeRouteIdentityKey(route)
	if tombstone, ok := s.tombstoneSeq[key]; ok && route.OwnerSeq <= tombstone {
		return RegisterResult{}, ErrStaleRoute
	}
	if route.OwnerSeq < s.ownerSeq[key] {
		return RegisterResult{}, ErrStaleRoute
	}
	s.ownerSeq[key] = route.OwnerSeq
	route = normalizeRouteSeen(route)

	conflictKeys := s.conflictsLocked(route)
	if len(conflictKeys) == 0 {
		s.upsertActiveLocked(route)
		return RegisterResult{}, nil
	}

	token := s.nextPendingToken()
	s.pending[token] = pendingRoute{
		route:     route,
		conflicts: conflictKeys,
	}
	actions := make([]RouteAction, 0, len(conflictKeys))
	for _, conflictKey := range conflictKeys {
		actions = append(actions, actionForReplacement(route, s.active[conflictKey]))
	}
	return RegisterResult{
		PendingToken: token,
		Actions:      actions,
	}, nil
}

func (s *authoritySlot) commitRouteLocked(token PendingRouteToken, nowUnixMS int64) error {
	pending, ok := s.pending[token]
	if !ok {
		return ErrRouteNotReady
	}
	if !s.routeAdmittedLocked(pending.route, nowUnixMS) {
		delete(s.pending, token)
		return ErrStaleRoute
	}
	key := makeRouteIdentityKey(pending.route)
	if tombstone, ok := s.tombstoneSeq[key]; ok && pending.route.OwnerSeq <= tombstone {
		delete(s.pending, token)
		return ErrStaleRoute
	}
	if pending.route.OwnerSeq < s.ownerSeq[key] {
		delete(s.pending, token)
		return ErrStaleRoute
	}
	acknowledged := make(map[identityKey]struct{}, len(pending.conflicts))
	for _, key := range pending.conflicts {
		acknowledged[key] = struct{}{}
	}
	for _, key := range s.conflictsLocked(pending.route) {
		if _, ok := acknowledged[key]; !ok {
			return ErrRouteNotReady
		}
	}
	for _, key := range pending.conflicts {
		if existing, ok := s.active[key]; ok {
			s.removeActiveLocked(key, existing)
		}
	}
	s.upsertActiveLocked(pending.route)
	delete(s.pending, token)
	return nil
}

func (s *authoritySlot) touchLocked(route Route, nowUnixMS int64) {
	if route.UID == "" {
		return
	}
	if !s.routeAdmittedLocked(route, nowUnixMS) {
		key := makeRouteIdentityKey(route)
		if existing, ok := s.active[key]; ok && existing.OwnerSeq <= route.OwnerSeq {
			s.removeActiveLocked(key, existing)
		}
		return
	}
	key := makeRouteIdentityKey(route)
	if tombstone, ok := s.tombstoneSeq[key]; ok && route.OwnerSeq <= tombstone {
		return
	}
	if route.OwnerSeq < s.ownerSeq[key] {
		return
	}
	s.ownerSeq[key] = route.OwnerSeq
	route = normalizeRouteSeen(route)
	if existing, ok := s.active[key]; ok {
		if route.LastSeenUnix < existing.LastSeenUnix {
			route.LastSeenUnix = existing.LastSeenUnix
		}
		s.upsertActiveLocked(route)
		return
	}
	if len(s.conflictsLocked(route)) != 0 {
		return
	}
	s.upsertActiveLocked(route)
}

// AdvanceCredentialFence atomically advances admission state and removes stale active/pending routes.
func (d *Directory) AdvanceCredentialFence(target RouteTarget, fence CredentialFence) (CredentialFenceAdvanceResult, error) {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return CredentialFenceAdvanceResult{}, err
	}
	return slot.advanceCredentialFenceLocked(fence, time.Now().UnixMilli())
}

func (s *authoritySlot) advanceCredentialFenceLocked(fence CredentialFence, nowUnixMS int64) (CredentialFenceAdvanceResult, error) {
	key := credentialFenceKey{uid: fence.UID, flag: fence.DeviceFlag}
	current, exists := s.credentialFences[key]
	if exists && fence.CredentialVersion < current.CredentialVersion {
		return CredentialFenceAdvanceResult{CurrentVersion: current.CredentialVersion}, nil
	}
	if exists && fence.CredentialVersion == current.CredentialVersion && !sameCredentialFence(current, fence) {
		return CredentialFenceAdvanceResult{CurrentVersion: current.CredentialVersion}, ErrCredentialFenceConflict
	}
	if err := validateCredentialFence(fence); err != nil {
		return CredentialFenceAdvanceResult{}, err
	}
	s.credentialFences[key] = fence
	result := CredentialFenceAdvanceResult{CurrentVersion: fence.CredentialVersion}
	for routeKey := range s.byUID[fence.UID] {
		route, ok := s.active[routeKey]
		if !ok || route.DeviceFlag != fence.DeviceFlag || routeMatchesFence(route, fence, nowUnixMS) {
			continue
		}
		s.rememberCredentialActionLocked(fence.DeviceFlag, actionForCredentialFence(fence, route))
		s.removeActiveLocked(routeKey, route)
		result.ActiveFenced++
	}
	for token, pending := range s.pending {
		route := pending.route
		if route.UID != fence.UID || route.DeviceFlag != fence.DeviceFlag || routeMatchesFence(route, fence, nowUnixMS) {
			continue
		}
		s.rememberCredentialActionLocked(fence.DeviceFlag, actionForCredentialFence(fence, route))
		delete(s.pending, token)
		result.PendingFenced++
	}
	result.Actions = s.credentialActionsForFenceLocked(key)
	return result, nil
}

// AcknowledgeCredentialAction removes one exact owner action only after the
// caller has proved local fencing/close or an exact stale no-op.
func (d *Directory) AcknowledgeCredentialAction(target RouteTarget, action RouteAction) error {
	shard := d.shard(target.HashSlot)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	slot, err := d.validateTargetLocked(shard, target)
	if err != nil {
		return err
	}
	key := makeCredentialActionKey(action)
	if retained, ok := slot.credentialActions[key]; ok && retained.action == action {
		delete(slot.credentialActions, key)
	}
	return nil
}

func (s *authoritySlot) rememberCredentialActionLocked(deviceFlag uint8, action RouteAction) {
	if s.credentialActions == nil {
		s.credentialActions = make(map[credentialActionKey]credentialAction)
	}
	s.credentialActions[makeCredentialActionKey(action)] = credentialAction{deviceFlag: deviceFlag, action: action}
}

func (s *authoritySlot) credentialActionsForFenceLocked(fenceKey credentialFenceKey) []RouteAction {
	actions := make([]RouteAction, 0)
	for _, retained := range s.credentialActions {
		if retained.action.UID == fenceKey.uid && retained.deviceFlag == fenceKey.flag {
			actions = append(actions, retained.action)
		}
	}
	sort.Slice(actions, func(i, j int) bool {
		if actions[i].SessionID != actions[j].SessionID {
			return actions[i].SessionID < actions[j].SessionID
		}
		return actions[i].OwnerNodeID < actions[j].OwnerNodeID
	})
	return actions
}

func makeCredentialActionKey(action RouteAction) credentialActionKey {
	return credentialActionKey{
		uid: action.UID, ownerNodeID: action.OwnerNodeID, ownerBootID: action.OwnerBootID,
		sessionID: action.SessionID, expectedOwnerSeq: action.ExpectedOwnerSeq,
		expectedCredentialVersion: action.ExpectedCredentialVersion,
		expectedLoginSessionID:    action.ExpectedLoginSessionID,
	}
}

func (s *authoritySlot) mergeLoadedFenceLocked(fence CredentialFence) error {
	if err := validateCredentialFence(fence); err != nil {
		return err
	}
	key := credentialFenceKey{uid: fence.UID, flag: fence.DeviceFlag}
	current, exists := s.credentialFences[key]
	if exists && current.CredentialVersion > fence.CredentialVersion {
		return ErrStaleRoute
	}
	if exists && current.CredentialVersion == fence.CredentialVersion && !sameCredentialFence(current, fence) {
		return ErrCredentialFenceConflict
	}
	s.credentialFences[key] = fence
	return nil
}

func (s *authoritySlot) routeAdmittedLocked(route Route, nowUnixMS int64) bool {
	fence, ok := s.credentialFences[credentialFenceKey{uid: route.UID, flag: route.DeviceFlag}]
	if !ok {
		return route.CredentialVersion == 0
	}
	return routeMatchesFence(route, fence, nowUnixMS)
}

func validateCredentialFence(fence CredentialFence) error {
	if fence.UID == "" || fence.CredentialVersion == 0 || fence.LoginSessionID == "" {
		return ErrRouteNotReady
	}
	switch fence.Status {
	case CredentialStatusActive:
		if fence.ExpiresAtUnixMS <= 0 {
			return ErrRouteNotReady
		}
	case CredentialStatusRevoked:
	default:
		return ErrRouteNotReady
	}
	return nil
}

func sameCredentialFence(left, right CredentialFence) bool {
	return left.UID == right.UID && left.DeviceFlag == right.DeviceFlag &&
		left.CredentialVersion == right.CredentialVersion && left.LoginSessionID == right.LoginSessionID &&
		left.Status == right.Status && left.ExpiresAtUnixMS == right.ExpiresAtUnixMS
}

func routeMatchesFence(route Route, fence CredentialFence, nowUnixMS int64) bool {
	return fence.Status == CredentialStatusActive && fence.ExpiresAtUnixMS > nowUnixMS &&
		route.CredentialVersion == fence.CredentialVersion &&
		route.LoginSessionID == fence.LoginSessionID && route.ExpiresAtUnixMS == fence.ExpiresAtUnixMS
}

func actionForCredentialFence(fence CredentialFence, route Route) RouteAction {
	reason := fence.MachineReason
	if reason == "" {
		reason = "SESSION_REPLACED_SAME_DEVICE_CLASS"
	}
	kind := "kick_then_close"
	if reason == "CREDENTIAL_ROTATED" || reason == "CREDENTIAL_RECONCILED" {
		kind = "close"
	}
	return RouteAction{
		UID: route.UID, OwnerNodeID: route.OwnerNodeID, OwnerBootID: route.OwnerBootID,
		SessionID: route.SessionID, ExpectedOwnerSeq: route.OwnerSeq,
		ExpectedCredentialVersion: route.CredentialVersion, ExpectedLoginSessionID: route.LoginSessionID,
		Kind: kind, Reason: reason,
	}
}

func (s *authoritySlot) conflictsLocked(route Route) []identityKey {
	keys := s.byUID[route.UID]
	if len(keys) == 0 {
		return nil
	}
	incomingKey := makeRouteIdentityKey(route)
	conflictKeys := make([]identityKey, 0, len(keys))
	for key := range keys {
		if key == incomingKey {
			continue
		}
		existing := s.active[key]
		if conflicts(route, existing) {
			conflictKeys = append(conflictKeys, key)
		}
	}
	sort.Slice(conflictKeys, func(i, j int) bool {
		return lessIdentityKey(conflictKeys[i], conflictKeys[j])
	})
	return conflictKeys
}

func (s *authoritySlot) upsertActiveLocked(route Route) {
	route = normalizeRouteSeen(route)
	key := makeRouteIdentityKey(route)
	if existing, ok := s.active[key]; ok {
		s.removeActiveLocked(key, existing)
	}
	s.active[key] = route
	if s.byUID[route.UID] == nil {
		s.byUID[route.UID] = make(map[identityKey]struct{})
	}
	s.byUID[route.UID][key] = struct{}{}
	s.scheduleExpiryLocked(key, route)
}

func (s *authoritySlot) removeActiveLocked(key identityKey, route Route) {
	s.unscheduleExpiryLocked(key)
	delete(s.active, key)
	if routes := s.byUID[route.UID]; routes != nil {
		delete(routes, key)
		if len(routes) == 0 {
			delete(s.byUID, route.UID)
		}
	}
}

func (s *authoritySlot) nextPendingToken() PendingRouteToken {
	s.nextID++
	return PendingRouteToken(fmt.Sprintf("%d", s.nextID))
}

func sameAuthorityIdentity(left, right RouteTarget) bool {
	return left.HashSlot == right.HashSlot &&
		left.SlotID == right.SlotID &&
		left.LeaderNodeID == right.LeaderNodeID &&
		left.LeaderTerm == right.LeaderTerm &&
		left.ConfigEpoch == right.ConfigEpoch
}

func conflicts(incoming, existing Route) bool {
	if incoming.UID != existing.UID || incoming.DeviceFlag != existing.DeviceFlag {
		return false
	}
	switch incoming.DeviceLevel {
	case deviceLevelMaster:
		return true
	case deviceLevelSlave:
		return incoming.DeviceID == existing.DeviceID
	default:
		return false
	}
}

func actionForReplacement(incoming, existing Route) RouteAction {
	kind := "close"
	if incoming.DeviceLevel == deviceLevelMaster && incoming.LoginSessionID != existing.LoginSessionID {
		kind = "kick_then_close"
	}
	return RouteAction{
		UID:                       existing.UID,
		OwnerNodeID:               existing.OwnerNodeID,
		OwnerBootID:               existing.OwnerBootID,
		SessionID:                 existing.SessionID,
		ExpectedOwnerSeq:          existing.OwnerSeq,
		ExpectedCredentialVersion: existing.CredentialVersion,
		ExpectedLoginSessionID:    existing.LoginSessionID,
		Kind:                      kind,
		Reason:                    "presence_conflict",
	}
}

func normalizeRouteSeen(route Route) Route {
	if route.LastSeenUnix == 0 {
		route.LastSeenUnix = route.ConnectedUnix
	}
	return route
}

func makeRouteIdentityKey(route Route) identityKey {
	return identityKey{
		uid:         route.UID,
		ownerNodeID: route.OwnerNodeID,
		ownerBootID: route.OwnerBootID,
		sessionID:   route.SessionID,
	}
}

func makeIdentityKey(identity RouteIdentity) identityKey {
	return identityKey{
		uid:         identity.UID,
		ownerNodeID: identity.OwnerNodeID,
		ownerBootID: identity.OwnerBootID,
		sessionID:   identity.SessionID,
	}
}

func sortRoutes(routes []Route) {
	if len(routes) < 2 {
		return
	}
	sort.Slice(routes, func(i, j int) bool {
		left := makeRouteIdentityKey(routes[i])
		right := makeRouteIdentityKey(routes[j])
		return lessIdentityKey(left, right)
	})
}

func lessIdentityKey(left, right identityKey) bool {
	if left.uid != right.uid {
		return left.uid < right.uid
	}
	if left.sessionID != right.sessionID {
		return left.sessionID < right.sessionID
	}
	if left.ownerNodeID != right.ownerNodeID {
		return left.ownerNodeID < right.ownerNodeID
	}
	if left.ownerBootID != right.ownerBootID {
		return left.ownerBootID < right.ownerBootID
	}
	return false
}
