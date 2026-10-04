package migration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func recoveryFixture(t *testing.T) (Workspace, SourceCapture, HistoryRecovery, *prefixFixture) {
	t.Helper()
	w := dedupeTestWorkspace(t)
	f := newPrefixFixture()
	f.messages[3] = append([]MessageEvidence(nil), f.messages[2]...)
	f.tails[3] = 3
	f.messages[1] = nil
	delete(f.tails, 1)
	capture := f.capture(t, w)
	var pin HistoryRecovery
	require.NoError(t, InspectCapturedHistoryPrefixes(context.Background(), capture, w, prefixFixtureDecoder{}, []uint64{7}, func(p HistoryPrefixReport) error {
		pin = HistoryRecovery{Owner: p.Owner, IdentitySHA256: p.IdentitySHA256, CaptureDigest: p.CaptureDigest, ProofDigest: p.Digest, SourceNode: 2, Messages: 3, HistorySHA256: p.Histories[1].SHA256}
		return nil
	}))
	putPrefixCandidates(t, w, f)
	return w, capture, pin, f
}

func TestHistoryRecoverySelectsOnlyExplicitCompleteCopy(t *testing.T) {
	w, capture, pin, _ := recoveryFixture(t)
	ctx := context.Background()
	strict := &historyPrefixComparison{ctx: ctx, capture: capture, w: w, decoder: prefixFixtureDecoder{}}
	require.ErrorContains(t, compareCandidatesWithHistory(ctx, w, "messages", &captureBatch{ctx: ctx, workspace: w}, strict.source), "configured_leader_is_not_complete")
	var previous *HistoryPrefixSelection
	for repeat := 0; repeat < 2; repeat++ {
		c := &historyPrefixComparison{ctx: ctx, capture: capture, w: w, decoder: prefixFixtureDecoder{}, policy: HistoryPolicy{LeaderQuorumPrefixes: true, Recoveries: []HistoryRecovery{pin}}}
		b := &captureBatch{ctx: ctx, workspace: w}
		require.NoError(t, compareCandidatesWithHistory(ctx, w, "messages", b, c.source))
		require.NoError(t, b.flush())
		count := 0
		require.NoError(t, WalkSelectedSources(ctx, w, func(r SelectedRecord) error {
			require.EqualValues(t, 2, r.NodeID)
			m, err := prefixFixtureDecoder{}.InspectMessage(r.Row, 1)
			require.NoError(t, err)
			count++
			require.EqualValues(t, count, m.Sequence)
			require.EqualValues(t, 100+count, m.ID)
			return nil
		}))
		require.Equal(t, 3, count)
		report, err := c.report()
		require.NoError(t, err)
		require.EqualValues(t, 1, report.Recovered)
		require.EqualValues(t, 1, report.Accepted)
		require.Zero(t, report.Unresolved)
		if previous != nil {
			require.Equal(t, previous, report)
		}
		previous = report
	}
}

func TestHistoryRecoveryRejectsDecisionDrift(t *testing.T) {
	for _, mode := range []string{"capture", "proof", "identity", "source", "count", "history", "unknown_owner"} {
		t.Run(mode, func(t *testing.T) {
			w, capture, pin, _ := recoveryFixture(t)
			switch mode {
			case "capture":
				pin.CaptureDigest = diagnosticSHA([]byte("other"))
			case "proof":
				pin.ProofDigest = diagnosticSHA([]byte("other"))
			case "identity":
				pin.IdentitySHA256 = diagnosticSHA([]byte("other"))
			case "source":
				pin.SourceNode = 1
			case "count":
				pin.Messages++
			case "history":
				pin.HistorySHA256 = diagnosticSHA([]byte("other"))
			case "unknown_owner":
				pin.Owner++
			}
			ctx := context.Background()
			c := &historyPrefixComparison{ctx: ctx, capture: capture, w: w, decoder: prefixFixtureDecoder{}, policy: HistoryPolicy{LeaderQuorumPrefixes: true, Recoveries: []HistoryRecovery{pin}}}
			require.Error(t, compareCandidatesWithHistory(ctx, w, "messages", &captureBatch{ctx: ctx, workspace: w}, c.source))
		})
	}
}

func TestHistoryRecoveryRejectsUnusedAndMalformedDecisions(t *testing.T) {
	w, capture, pin, _ := recoveryFixture(t)
	p := HistoryPolicy{LeaderQuorumPrefixes: true, Recoveries: []HistoryRecovery{pin}}
	require.NoError(t, validateHistoryPolicy(&p))
	c := &historyPrefixComparison{ctx: context.Background(), capture: capture, w: w, decoder: prefixFixtureDecoder{}, policy: p}
	_, err := c.report()
	require.ErrorContains(t, err, "not applied")
	p.LeaderQuorumPrefixes = false
	require.Error(t, validateHistoryPolicy(&p))
	p.LeaderQuorumPrefixes = true
	p.Recoveries = append(p.Recoveries, pin)
	require.Error(t, validateHistoryPolicy(&p))
	p.Recoveries = p.Recoveries[:1]
	p.Recoveries[0].ProofDigest = "not-a-digest"
	require.Error(t, validateHistoryPolicy(&p))
}

// Even a decision pinned to freshly inspected evidence cannot authorize
// destructive lineage, conflicting contents, a partial leader or a minority.
func TestHistoryRecoveryCannotApproveOtherFailures(t *testing.T) {
	for _, mode := range []string{"deletion", "payload_conflict", "partial_leader", "minority"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			w := dedupeTestWorkspace(t)
			f := newPrefixFixture()
			f.messages[3] = append([]MessageEvidence(nil), f.messages[2]...)
			f.tails[3] = 3
			f.messages[1] = nil
			delete(f.tails, 1)
			switch mode {
			case "deletion":
				for node, logs := range f.logs {
					deleted := logs[0]
					deleted.Index = 9
					deleted.Deleted = true
					f.logs[node] = append([]ChannelConfigLog{deleted}, logs...)
				}
			case "payload_conflict":
				f.messages[3][0].SHA256 = diagnosticSHA([]byte("different payload"))
			case "partial_leader":
				f.messages[1] = append([]MessageEvidence(nil), f.messages[2][:1]...)
				f.tails[1] = 1
			case "minority":
				f.messages[3] = nil
				delete(f.tails, 3)
			}
			capture := f.capture(t, w)
			var pin HistoryRecovery
			require.NoError(t, InspectCapturedHistoryPrefixes(ctx, capture, w, prefixFixtureDecoder{}, []uint64{7}, func(p HistoryPrefixReport) error {
				pin = HistoryRecovery{Owner: p.Owner, IdentitySHA256: p.IdentitySHA256, CaptureDigest: p.CaptureDigest, ProofDigest: p.Digest, SourceNode: 2, Messages: 3, HistorySHA256: p.Histories[1].SHA256}
				return nil
			}))
			c := &historyPrefixComparison{ctx: ctx, capture: capture, w: w, decoder: prefixFixtureDecoder{}, policy: HistoryPolicy{LeaderQuorumPrefixes: true, Recoveries: []HistoryRecovery{pin}}}
			row := sourceCandidate{NodeID: 2, Table: "Message", Identity: RecordIdentity{ChannelHash: 7, Channel: ChannelIdentity{ID: "channel", Type: 2}}, Group: sourceGroup{Leader: 1, Replicas: []uint64{1, 2, 3}}}
			require.ErrorContains(t, c.accept(row), "history recovery evidence mismatch")
		})
	}
}
