package commit

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/stretchr/testify/require"
)

func TestGroupBuildReconstructionIsBoundedAndPreservesLogicalOutcomes(t *testing.T) {
	for _, failFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid-built-first", true: "rejected-first"}[failFirst], func(t *testing.T) {
			db, err := engine.Open(filepath.Join(t.TempDir(), "db"), engine.Options{})
			require.NoError(t, err)
			defer db.Close()
			commits, builds, publishes, finalized := 0, 0, 0, 0
			c := &Coordinator{db: db, stopCh: make(chan struct{}), commitFunc: func(b *engine.Batch) error { commits++; return b.Commit(true) }}
			good := newPendingRequest(Request{RebuildOnGroupAbort: true, Build: func(b *engine.Batch) error { builds++; return b.Set([]byte("valid"), []byte("value")) }, Publish: func() error { publishes++; return nil }, Finalize: func() { finalized++ }})
			cause := errors.New("request validation rejected")
			bad := newPendingRequest(Request{RebuildOnGroupAbort: true, Build: func(b *engine.Batch) error {
				require.NoError(t, b.Set([]byte("partial"), []byte("must remain absent")))
				return cause
			}, Finalize: func() { finalized++ }})
			requests := []pendingRequest{good, bad}
			if failFirst {
				requests = []pendingRequest{bad, good}
			}
			c.commit(requestBatch{requests: requests}, 0)
			result := <-good.done
			require.NoError(t, result.Err)
			require.Equal(t, OutcomeCommitted, result.Outcome)
			rejected := <-bad.done
			require.ErrorIs(t, rejected.Err, cause)
			require.Equal(t, OutcomeDefinitelyNotCommitted, rejected.Outcome)
			require.Equal(t, 1, commits)
			require.Equal(t, 1, publishes)
			require.Equal(t, 2, finalized)
			require.Equal(t, map[bool]int{false: 2, true: 1}[failFirst], builds)
			_, found, err := db.Get([]byte("partial"))
			require.NoError(t, err)
			require.False(t, found)
		})
	}
}

func TestGroupReconstructionNeverRetriesPhysicalCommitFailure(t *testing.T) {
	db, err := engine.Open(filepath.Join(t.TempDir(), "db"), engine.Options{})
	require.NoError(t, err)
	defer db.Close()
	cause := errors.New("ambiguous physical commit")
	commits, builds := 0, 0
	c := &Coordinator{db: db, stopCh: make(chan struct{}), commitFunc: func(*engine.Batch) error { commits++; return cause }}
	r := newPendingRequest(Request{RebuildOnGroupAbort: true, Build: func(b *engine.Batch) error { builds++; return b.Set([]byte("valid"), []byte("value")) }})
	c.commit(requestBatch{requests: []pendingRequest{r}}, 0)
	result := <-r.done
	require.Equal(t, OutcomeUnknown, result.Outcome)
	require.ErrorIs(t, result.Err, cause)
	require.Equal(t, 1, builds)
	require.Equal(t, 1, commits)
}
