package online

// ActiveRoutesByUIDs reads only indexed active routes for a bounded UID batch.
// The complete flag is false on output saturation; partial rows cannot prove offline.
func (r *Registry) ActiveRoutesByUIDs(uids []string, limit int) ([]OwnerRoute, bool) {
	if r == nil || limit <= 0 || len(uids) > 512 {
		return nil, false
	}
	var routes []OwnerRoute
	for i := range r.shards {
		shard := &r.shards[i]
		shard.mu.RLock()
		for _, uid := range uids {
			for id := range shard.activeByUID[uid] {
				session, ok := shard.bySession[id]
				if !ok || session.State != RouteStateActive {
					continue
				}
				if len(routes) == limit {
					shard.mu.RUnlock()
					return nil, false
				}
				routes = append(routes, session.Route)
			}
		}
		shard.mu.RUnlock()
	}
	return routes, true
}

func (s *registryShard) removeActiveUID(session LocalSession) {
	uid := session.Route.UID
	ids := s.activeByUID[uid]
	delete(ids, session.Route.SessionID)
	if len(ids) == 0 {
		delete(s.activeByUID, uid)
	}
}
