package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeduplicateUIDsPreservesFirstOccurrenceOrder(t *testing.T) {
	require.Equal(t,
		[]string{"uid-2", "uid-1", "uid-3"},
		deduplicateUIDs([]string{"uid-2", "uid-1", "uid-2", "uid-3", "uid-1"}),
	)
	require.Empty(t, deduplicateUIDs(nil))
}
