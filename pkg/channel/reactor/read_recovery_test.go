package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestRuntimeProbeRequiresCompletedQuorumRecovery(t *testing.T) {
	log := newBlockingReactorQuorumLog()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16,
		Store: store.NewMemoryFactory(), QuorumLog: log})
	require.NoError(t, err)
	defer g.Close()
	// Unblock worker cleanup even when an assertion fails before release.
	released := false
	defer func() {
		if !released {
			close(log.release)
		}
	}()
	meta := testMeta("read-recovery-pending", 1, 1)
	meta.RouteGeneration = 7
	meta.Replicas, meta.ISR, meta.MinISR = []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2
	future, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	select {
	case <-log.started:
	case <-time.After(time.Second):
		t.Fatal("quorum recovery did not start")
	}
	probe, err := g.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)
	require.Len(t, probe.Channels, 1)
	require.True(t, probe.Channels[0].RecoveryRequired)
	close(log.release)
	released = true
	require.NoError(t, awaitFutureResult(t, future).Err)
	probe, err = g.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)
	require.False(t, probe.Channels[0].RecoveryRequired)
}

func TestRuntimeProbeFailedInstallRemainsUnreadableAndWriteFenceAllowsRecoveredReads(t *testing.T) {
	log := &reactorCaptureQuorumLog{installErr: ch.ErrNotReady}
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16,
		Store: store.NewMemoryFactory(), QuorumLog: log})
	require.NoError(t, err)
	defer g.Close()
	meta := testMeta("read-recovery-failed", 1, 1)
	meta.RouteGeneration = 7
	meta.WriteFence = ch.WriteFence{Token: "read-only", Version: 1}
	require.ErrorIs(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}), ch.ErrNotReady)
	probe, err := g.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)
	require.True(t, probe.Channels[0].RecoveryRequired)
	log.installErr = nil
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))
	probe, err = g.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)
	require.False(t, probe.Channels[0].RecoveryRequired)
	require.Equal(t, meta.WriteFence, probe.Channels[0].WriteFence)
}
