package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func putPrefixCandidates(t *testing.T, w Workspace, f *prefixFixture) {
	t.Helper()
	ctx := context.Background()
	for n := uint64(1); n <= 3; n++ {
		for _, m := range f.messages[n] {
			row := sourceCandidate{NodeID: n, SourceKey: sourceRowKey(n, Row{Key: prefixFixtureDecoder{}.HistoryMessageKey(7, m.Sequence)}), Table: "Message", Kind: Primary, Identity: RecordIdentity{ChannelHash: 7, Channel: ChannelIdentity{ID: "channel", Type: 2}}, LogicalKey: IdentityKey("channel", uint8(2), m.Sequence), Digest: m.SHA256, Group: sourceGroup{Leader: 1, Replicas: []uint64{1, 2, 3}}}
			v, e := MarshalState(row)
			require.NoError(t, e)
			require.NoError(t, w.Put(ctx, []transfer.SpoolRow{{Key: candidateKey("messages", n, row.Table, row.LogicalKey), Value: v}}))
		}
	}
}

func TestHistoryPrefixSelectionKeepsOriginalLeaderAndRebuildsProofs(t *testing.T) {
	ctx := context.Background()
	w := dedupeTestWorkspace(t)
	f := newPrefixFixture()
	capture := f.capture(t, w)
	putPrefixCandidates(t, w, f)
	// This is the actual pre-existing comparison failure, independently of the
	// new diagnostic classifier. Default strict behavior must still reproduce it.
	require.ErrorContains(t, compareCandidates(ctx, w, "messages", &captureBatch{ctx: ctx, workspace: w}), "missing on configured replica node 3")
	var previous *HistoryPrefixSelection
	for repeat := 0; repeat < 2; repeat++ {
		comparison := &historyPrefixComparison{ctx: ctx, capture: capture, w: w, decoder: prefixFixtureDecoder{}}
		b := &captureBatch{ctx: ctx, workspace: w}
		require.NoError(t, compareCandidatesWithHistory(ctx, w, "messages", b, comparison.source))
		require.NoError(t, b.flush())
		report, err := comparison.report()
		require.NoError(t, err)
		require.EqualValues(t, 1, report.Channels)
		require.EqualValues(t, 1, report.Accepted)
		require.Zero(t, report.Unresolved)
		if previous != nil {
			require.Equal(t, previous, report)
		}
		previous = report
		count := 0
		require.NoError(t, WalkSelectedSources(ctx, w, func(r SelectedRecord) error {
			require.EqualValues(t, 1, r.NodeID)
			m, e := prefixFixtureDecoder{}.InspectMessage(r.Row, 1)
			require.NoError(t, e)
			require.Equal(t, f.messages[1][count], m)
			count++
			return nil
		}))
		require.Equal(t, 3, count)
	}
}

func TestHistoryPrefixSelectionDoesNotReusePriorProofAfterCapturedRowChanges(t *testing.T) {
	ctx := context.Background()
	w := dedupeTestWorkspace(t)
	f := newPrefixFixture()
	capture := f.capture(t, w)
	putPrefixCandidates(t, w, f)
	comparison := &historyPrefixComparison{ctx: ctx, capture: capture, w: w, decoder: prefixFixtureDecoder{}}
	require.NoError(t, compareCandidatesWithHistory(ctx, w, "messages", &captureBatch{ctx: ctx, workspace: w}, comparison.source))
	// Keep the old scratch proof, then alter a primary field in the captured
	// lagging replica. A new invocation must inspect rows again and reject it.
	m := f.messages[3][0]
	m.SHA256 = diagnosticSHA([]byte("payload changed"))
	data, e := json.Marshal(m)
	require.NoError(t, e)
	r := Row{Table: "Message", Kind: Primary, Owner: 7, ID: m.Sequence, Key: prefixFixtureDecoder{}.HistoryMessageKey(7, m.Sequence), Value: data}
	data, e = json.Marshal(r)
	require.NoError(t, e)
	require.ErrorContains(t, w.Put(ctx, []transfer.SpoolRow{{Key: sourceRowKey(3, r), Value: data}}), "durable key conflict")
	altered := prefixRowDriftWorkspace{Workspace: w, key: sourceRowKey(3, r), value: data}
	comparison = &historyPrefixComparison{ctx: ctx, capture: capture, w: altered, decoder: prefixFixtureDecoder{}}
	require.ErrorContains(t, compareCandidatesWithHistory(ctx, w, "messages", &captureBatch{ctx: ctx, workspace: w}, comparison.source), "history_is_not_an_exact_prefix")
}

type prefixRowDriftWorkspace struct {
	Workspace
	key, value []byte
}

func (w prefixRowDriftWorkspace) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if bytes.Equal(key, w.key) {
		return w.value, true, nil
	}
	return w.Workspace.Get(ctx, key)
}
func (w prefixRowDriftWorkspace) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	return w.Workspace.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		if bytes.Equal(row.Key, w.key) {
			row.Value = w.value
		}
		return visit(row)
	})
}

func TestHistoryPrefixRejectsCaptureAndCandidateScopeDrift(t *testing.T) {
	for _, mode := range []string{"command_digest", "slot_count", "source_inventory", "identity", "replicas", "leader", "table"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			w := dedupeTestWorkspace(t)
			f := newPrefixFixture()
			capture := f.capture(t, w)
			row := sourceCandidate{NodeID: 1, Table: "Message", Identity: RecordIdentity{ChannelHash: 7, Channel: ChannelIdentity{ID: "channel", Type: 2}}, Group: sourceGroup{Leader: 1, Replicas: []uint64{1, 2, 3}}}
			switch mode {
			case "command_digest":
				capture.Authority[0].SHA256 = "wrong"
			case "slot_count":
				for i := range capture.Nodes {
					capture.Nodes[i].Config.SlotCount = 0
				}
			case "source_inventory":
				capture.Authority = capture.Authority[:2]
			case "identity":
				row.Identity.Channel.ID = "other"
			case "replicas":
				row.Group.Replicas = []uint64{1, 2}
			case "leader":
				row.Group.Leader = 2
			case "table":
				row.Table = "User"
			}
			c := &historyPrefixComparison{ctx: ctx, capture: capture, w: w, decoder: prefixFixtureDecoder{}}
			require.Error(t, c.accept(row), fmt.Sprint(mode))
		})
	}
}
