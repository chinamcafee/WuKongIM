package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestRepairScannerProgressesPastFirstSlotAcrossTicks(t *testing.T) {
	id := ch.ChannelID{ID: "later-slot", Type: 1}
	source := newFakeRepairScannerSource(id)
	source.localSlots = []uint32{1, 2}
	source.slotPages[2] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(id)}}
	store := &fakeRepairScannerStore{}
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 64, MaxPagesPerTick: 1, MaxTasksPerTick: 1}, source, store)
	for range 2 {
		result, err := scanner.RunOnce(context.Background())
		require.NoError(t, err)
		require.LessOrEqual(t, result.PagesScanned, 1)
	}
	require.Len(t, store.requests, 1, "the default one-page budget must not starve later Slots")
}

func TestRepairScannerContinuesLaterPagesAcrossTicks(t *testing.T) {
	id := ch.ChannelID{ID: "later-page", Type: 1}
	source := newFakeRepairScannerSource(id)
	first := ch.ChannelID{ID: "already-active", Type: 1}
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(first)}, {failoverPlannerMeta(id)}}
	source.active[first] = true
	store := &fakeRepairScannerStore{}
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 64, MaxPagesPerTick: 1, MaxTasksPerTick: 1}, source, store)
	for range 2 {
		_, err := scanner.RunOnce(context.Background())
		require.NoError(t, err)
	}
	require.Len(t, store.requests, 1, "page cursors must survive a bounded tick")
}

// Row cursors model the real metadata API, including a task budget cutting a page.
type rowCursorRepairSource struct {
	*fakeRepairScannerSource
	rows map[uint32][]metadb.ChannelRuntimeMeta
}

func (s *rowCursorRepairSource) ListRepairScannerRuntimeMetaPage(_ context.Context, slot uint32, after metadb.ChannelRuntimeMetaCursor, limit int) ([]RepairScannerRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	remaining := make([]metadb.ChannelRuntimeMeta, 0)
	for _, row := range s.rows[slot] {
		if row.ChannelID > after.ChannelID {
			remaining = append(remaining, row)
		}
	}
	done := len(remaining) <= limit
	if !done {
		remaining = remaining[:limit]
	}
	result := make([]RepairScannerRuntimeMeta, 0, len(remaining))
	next := after
	for _, row := range remaining {
		result = append(result, RepairScannerRuntimeMeta{Meta: row})
		next = metadb.ChannelRuntimeMetaCursor{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
	}
	return result, next, done, nil
}

func TestRepairScannerTaskBudgetResumesInsidePage(t *testing.T) {
	a, b := ch.ChannelID{ID: "a", Type: 1}, ch.ChannelID{ID: "b", Type: 1}
	source := &rowCursorRepairSource{newFakeRepairScannerSource(a, b), map[uint32][]metadb.ChannelRuntimeMeta{
		1: {failoverPlannerMeta(a), failoverPlannerMeta(b)},
	}}
	store := &fakeRepairScannerStore{}
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 64, MaxPagesPerTick: 1, MaxTasksPerTick: 1}, source, store)
	for range 2 {
		result, err := scanner.RunOnce(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, result.TasksCreated)
	}
	require.Len(t, store.requests, 2)
	require.Equal(t, a, store.requests[0].ChannelID)
	require.Equal(t, b, store.requests[1].ChannelID)
}

func TestRepairScannerDropsCursorAfterLeadershipLoss(t *testing.T) {
	a, b := ch.ChannelID{ID: "a", Type: 1}, ch.ChannelID{ID: "b", Type: 1}
	source := &rowCursorRepairSource{newFakeRepairScannerSource(a, b), map[uint32][]metadb.ChannelRuntimeMeta{
		1: {failoverPlannerMeta(a), failoverPlannerMeta(b)},
	}}
	store := &fakeRepairScannerStore{}
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 1, MaxPagesPerTick: 1, MaxTasksPerTick: 1}, source, store)
	_, err := scanner.RunOnce(context.Background())
	require.NoError(t, err)
	source.localSlots = nil
	_, err = scanner.RunOnce(context.Background())
	require.NoError(t, err)
	require.Empty(t, scanner.cursors)
	source.localSlots = []uint32{1}
	_, err = scanner.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, a, store.requests[1].ChannelID, "reacquired Slots must begin a fresh scan")
}

func TestRepairScannerRotatesLargeSlotPagesBeforeNextTick(t *testing.T) {
	a, b, c := ch.ChannelID{ID: "a", Type: 1}, ch.ChannelID{ID: "b", Type: 1}, ch.ChannelID{ID: "c", Type: 1}
	source := &rowCursorRepairSource{newFakeRepairScannerSource(a, b, c), map[uint32][]metadb.ChannelRuntimeMeta{
		1: {failoverPlannerMeta(a), failoverPlannerMeta(c)}, 2: {failoverPlannerMeta(b)},
	}}
	source.localSlots = []uint32{2, 1}
	store := &fakeRepairScannerStore{}
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 1, MaxPagesPerTick: 1, MaxTasksPerTick: 1}, source, store)
	for range 3 {
		_, err := scanner.RunOnce(context.Background())
		require.NoError(t, err)
	}
	require.Len(t, store.requests, 3)
	require.Equal(t, a, store.requests[0].ChannelID)
	require.Equal(t, b, store.requests[1].ChannelID)
	require.Equal(t, c, store.requests[2].ChannelID)
}
