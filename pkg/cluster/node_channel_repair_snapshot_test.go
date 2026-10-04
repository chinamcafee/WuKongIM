package cluster

import (
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestChannelRepairSnapshotReadsCurrentHealthWithoutPlacementRevisionChange(t *testing.T) {
	cached := control.Snapshot{Revision: 7, ControllerID: 2, Nodes: []control.Node{{NodeID: 1,
		Health: control.NodeHealth{Freshness: control.NodeHealthFresh, RuntimeReady: true, Status: control.NodeAlive},
	}}}
	current := cached.Clone()
	current.Nodes[0].Health.Freshness = control.NodeHealthStale
	node := &Node{control: control.NewStaticController(current), controlSnapshot: cached}
	node.started.Store(true)
	snapshot, err := node.ControlSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, control.NodeHealthStale, snapshot.Nodes[0].Health.Freshness,
		"repair must observe expired health even if the Node-applied placement snapshot is unchanged")
	require.Equal(t, control.NodeHealthFresh, node.controlSnapshot.Nodes[0].Health.Freshness)
}

func TestChannelRepairSnapshotDoesNotFallbackToCachedHealth(t *testing.T) {
	node := &Node{controlSnapshot: control.Snapshot{Revision: 7, ControllerID: 2}}
	node.started.Store(true)
	_, err := node.ControlSnapshot(context.Background())
	require.ErrorIs(t, err, ErrNotStarted)
}
