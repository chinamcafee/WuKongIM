package persistreceipt

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryResolveAndCancelAreNonBlocking(t *testing.T) {
	registry := NewRegistry()
	waiter := registry.Register("request-1")
	require.Equal(t, 1, registry.Len())
	require.True(t, registry.Resolve("request-1", Result{MessageID: 1, MessageSeq: 2}))
	require.Equal(t, Result{MessageID: 1, MessageSeq: 2}, <-waiter)
	require.Equal(t, 0, registry.Len())
	require.False(t, registry.Resolve("request-1", Result{}))

	registry.Register("request-2")
	registry.Cancel("request-2")
	require.Equal(t, 0, registry.Len())
	require.False(t, registry.Resolve("request-2", Result{}))
}
