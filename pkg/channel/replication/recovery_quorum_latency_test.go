package replication

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// delayedRecoveryVoter keeps accepted probes outstanding until the test releases
// them, modeling a paused process rather than an immediately failed connection.
type delayedRecoveryVoter struct {
	*scriptedRecoveryProbeDispatcher
	pending []func(ProbeResult, error)
}

func (d *delayedRecoveryVoter) submitRecoveryProbe(ctx context.Context, q recoveryProbeQuery, complete func(ProbeResult, error)) error {
	if q.Voter == 3 {
		d.pending = append(d.pending, complete)
		return nil
	}
	return d.scriptedRecoveryProbeDispatcher.submitRecoveryProbe(ctx, q, complete)
}

func TestRecoveryProbeOwnerUsesAvailableQuorumBeforePausedVoterReturns(t *testing.T) {
	entries := makeRecoveryChain(3)
	d := &delayedRecoveryVoter{scriptedRecoveryProbeDispatcher: &scriptedRecoveryProbeDispatcher{results: map[ch.NodeID]ProbeResult{
		1: recoveryReport(1, 3, 1, entries).Result,
		2: recoveryReport(2, 3, 1, entries).Result,
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		selection recoverySelection
		err       error
	}
	done := make(chan result, 1)
	go func() {
		s, e := recoverQuorumPrefix(ctx, recoveryProbeRequest{ChannelKey: "1:paused", ChannelID: ch.ChannelID{ID: "paused", Type: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute}, d)
		done <- result{s, e}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("recovery waited for the paused voter despite a complete surviving quorum")
	}
	if got.err != nil || got.selection.Index != 3 || len(got.selection.Supporters) != 2 {
		t.Fatalf("recovery = %+v, %v", got.selection, got.err)
	}
	if len(d.pending) != 1 {
		t.Fatalf("paused probes = %d, want one frontier probe", len(d.pending))
	}
	// Completion ownership survives the caller's return; the late callback must
	// fit its original bounded mailbox without blocking or changing the proof.
	d.pending[0](ProbeResult{}, context.DeadlineExceeded)
}
