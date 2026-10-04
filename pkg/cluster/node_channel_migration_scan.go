package cluster

import (
	"context"
	"sort"
	"sync"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type migrationTaskPageReader func(context.Context, uint16, metadb.ChannelMigrationTaskCursor, int) ([]metadb.ChannelMigrationTask, metadb.ChannelMigrationTaskCursor, bool, error)

// migrationTaskScan retains one small cursor per locally owned hash slot. Both
// busy shards and waiting/blocked tasks yield to later shards on every page.
type migrationTaskScan struct {
	mu      sync.Mutex
	next    uint16
	cursors map[uint16]metadb.ChannelMigrationTaskCursor
}

func (s *migrationTaskScan) page(ctx context.Context, hashSlots []uint16, limit int, read migrationTaskPageReader) ([]metadb.ChannelMigrationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cursors == nil {
		s.cursors = make(map[uint16]metadb.ChannelMigrationTaskCursor)
	}
	for hashSlot := range s.cursors {
		i := sort.Search(len(hashSlots), func(i int) bool { return hashSlots[i] >= hashSlot })
		if i == len(hashSlots) || hashSlots[i] != hashSlot {
			delete(s.cursors, hashSlot)
		}
	}
	if len(hashSlots) == 0 || limit <= 0 {
		return nil, nil
	}
	start := sort.Search(len(hashSlots), func(i int) bool { return hashSlots[i] >= s.next }) % len(hashSlots)
	out := make([]metadb.ChannelMigrationTask, 0, limit)
	for visited := 0; visited < len(hashSlots) && len(out) < limit; visited++ {
		i := (start + visited) % len(hashSlots)
		hashSlot := hashSlots[i]
		tasks, after, done, err := read(ctx, hashSlot, s.cursors[hashSlot], limit-len(out))
		s.next = hashSlots[(i+1)%len(hashSlots)]
		if err != nil {
			return out, err
		}
		if done {
			delete(s.cursors, hashSlot)
		} else {
			s.cursors[hashSlot] = after
		}
		out = append(out, tasks...)
	}
	return out, nil
}
