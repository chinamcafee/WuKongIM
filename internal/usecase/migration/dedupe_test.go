package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

type dedupeFixture struct {
	messages      []DedupeMessage
	fileVersion   string
	failAfterRows bool
}

func (f *dedupeFixture) ReadStoppedNode(_ context.Context, n NodeOptions, rows func(Row) error, files func(SourceFile) error) (NodeSnapshot, error) {
	file := SourceFile{Path: "immutable-source", Size: 1, SHA256: f.fileVersion}
	var b bytes.Buffer
	_ = json.NewEncoder(&b).Encode(file)
	if err := files(file); err != nil {
		return NodeSnapshot{}, err
	}
	for _, m := range f.messages {
		data, _ := json.Marshal(m)
		if err := rows(Row{Table: "Message", Kind: Primary, Owner: m.Owner, ID: m.Sequence, Value: data}); err != nil {
			return NodeSnapshot{}, err
		}
	}
	if f.failAfterRows {
		return NodeSnapshot{}, errors.New("source changed during scan")
	}
	return NodeSnapshot{NodeID: n.NodeID, DataDigest: diagnosticSHA(b.Bytes())}, nil
}

func (*dedupeFixture) InspectDedupeMessage(r Row, _ int) (m DedupeMessage, err error) {
	err = json.Unmarshal(r.Value, &m)
	return
}

func dedupeFact(owner, seq, id uint64, client string) DedupeMessage {
	return DedupeMessage{Owner: owner, ChannelSHA256: fmt.Sprint(owner), ClientKeySHA256: client, MessageEvidence: MessageEvidence{ID: id, Sequence: seq, SHA256: fmt.Sprintf("%d/%d/%d", owner, seq, id)}}
}

func dedupeTestWorkspace(t *testing.T) Workspace {
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "scratch"), "dedupe-test", 128<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Close()) })
	return w
}

func dedupeTestPlan(nodes int) Plan {
	p := Plan{Version: 1, SourceCommit: "original"}
	for i := 1; i <= nodes; i++ {
		p.Sources = append(p.Sources, NodeOptions{NodeID: uint64(i), Options: Options{ShardCount: 1}})
	}
	return p
}

func TestDedupeLatestSequenceUnionAndReplicaIsolation(t *testing.T) {
	// The two rules overlap on the same pair. Count its deletion only once.
	f := &dedupeFixture{messages: []DedupeMessage{dedupeFact(9, 3, 20, "retry"), dedupeFact(9, 1, 10, "other"), dedupeFact(9, 2, 20, "retry")}}
	f.messages[0].StreamParent = true
	w := dedupeTestWorkspace(t)
	var out bytes.Buffer
	p := dedupeTestPlan(2)
	r, err := PlanMessageDedupe(context.Background(), p, w, f, f, &out, nil)
	require.NoError(t, err)
	require.Equal(t, 0, r.CommandExitCode())
	require.True(t, r.RenumberingRequired)
	require.False(t, r.CutoverReady)
	require.Len(t, r.Nodes, 2)
	for _, n := range r.Nodes {
		require.EqualValues(t, 3, n.Messages)
		require.EqualValues(t, 1, n.MessageIDGroups)
		require.EqualValues(t, 1, n.ClientKeyGroups)
		require.EqualValues(t, 1, n.Dropped)
		require.EqualValues(t, 1, n.ChangedSequences)
		require.EqualValues(t, 1, n.StreamParents)
		require.Zero(t, n.DroppedStreamParents)
	}
	var drops []DedupeDrop
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'}) {
		var d DedupeDrop
		require.NoError(t, json.Unmarshal(line, &d))
		if d.Type == "candidate_drop" {
			drops = append(drops, d)
		}
	}
	require.Len(t, drops, 2)
	for _, d := range drops {
		require.EqualValues(t, 2, d.Message.Sequence)
		require.Len(t, d.Winners, 1)
		require.EqualValues(t, 3, d.Winners[0].Sequence)
		require.Equal(t, []string{"id", "client"}, d.Reasons)
		require.False(t, d.Unresolved)
	}
	require.Equal(t, diagnosticSHA(out.Bytes()), r.DetailsSHA256)
	first := out.String()
	out.Reset()
	repeat, err := PlanMessageDedupe(context.Background(), p, w, f, f, &out, nil)
	require.NoError(t, err)
	require.Equal(t, r, repeat)
	require.Equal(t, first, out.String())
	for _, key := range []string{"workflow/PREPARED", "workflow/CONVERTED"} {
		_, found, err := w.Get(context.Background(), []byte(key))
		require.NoError(t, err)
		require.False(t, found)
	}
}

func TestDedupeRejectsUnorderedAndOverlappingDecisions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []DedupeMessage
	}{
		{"cross_channel_id", []DedupeMessage{dedupeFact(1, 1, 1, ""), dedupeFact(2, 1, 1, "")}},
		{"superseded_winner", []DedupeMessage{dedupeFact(1, 1, 1, "a"), dedupeFact(1, 2, 1, "b"), dedupeFact(1, 3, 2, "b")}},
		{"source_gap", []DedupeMessage{dedupeFact(1, 2, 1, "")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &dedupeFixture{messages: tc.messages}
			var out bytes.Buffer
			r, err := PlanMessageDedupe(context.Background(), dedupeTestPlan(1), dedupeTestWorkspace(t), f, f, &out, nil)
			require.NoError(t, err)
			require.True(t, r.ScanComplete)
			require.Positive(t, r.Unresolved)
			require.Equal(t, 1, r.CommandExitCode())
			require.False(t, r.CutoverReady)
		})
	}
}

func TestDedupeEmptyKeysAndSeparateClientIdentitiesSurvive(t *testing.T) {
	f := &dedupeFixture{messages: []DedupeMessage{dedupeFact(1, 1, 1, ""), dedupeFact(1, 2, 2, ""), dedupeFact(1, 3, 3, "sender1"), dedupeFact(1, 4, 4, "sender2"), dedupeFact(2, 1, 5, "channel2")}}
	var out bytes.Buffer
	r, err := PlanMessageDedupe(context.Background(), dedupeTestPlan(1), dedupeTestWorkspace(t), f, f, &out, nil)
	require.NoError(t, err)
	require.Zero(t, r.Nodes[0].Dropped)
	require.Zero(t, r.Nodes[0].AffectedChannels)
	require.False(t, r.RenumberingRequired)
	var summary struct {
		Type     string               `json:"type"`
		Protocol DedupeProtocolImpact `json:"protocol_impact"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &summary))
	require.Equal(t, "message_protocol_impact", summary.Type)
	require.EqualValues(t, 5, summary.Protocol.Retained)
	require.Zero(t, summary.Protocol.RetainedUnsupported)
}

func TestDedupeBindsResumeToUnchangedSource(t *testing.T) {
	f := &dedupeFixture{messages: []DedupeMessage{dedupeFact(1, 1, 1, "")}}
	w := dedupeTestWorkspace(t)
	var out bytes.Buffer
	_, err := PlanMessageDedupe(context.Background(), dedupeTestPlan(1), w, f, f, &out, nil)
	require.NoError(t, err)
	f.fileVersion = "changed"
	r, err := PlanMessageDedupe(context.Background(), dedupeTestPlan(1), w, f, f, &out, nil)
	require.Error(t, err)
	require.False(t, r.ScanComplete)
	f.failAfterRows = true
	r, err = PlanMessageDedupe(context.Background(), dedupeTestPlan(1), dedupeTestWorkspace(t), f, f, &out, nil)
	require.ErrorContains(t, err, "source changed during scan")
	require.False(t, r.ScanComplete)
}

func TestDedupeProtocolImpactCountsOnlySurvivorsAsBlockers(t *testing.T) {
	f := &dedupeFixture{messages: []DedupeMessage{
		dedupeFact(9, 1, 10, "retry"), dedupeFact(9, 2, 10, "retry"),
		dedupeFact(9, 3, 30, "cmd"), dedupeFact(9, 4, 40, "stream"),
		dedupeFact(9, 5, 50, "a"), dedupeFact(9, 6, 60, "b"), dedupeFact(9, 7, 70, "c"),
	}}
	f.messages[0].UnsupportedFields = []string{"stream_id", "expire"}
	f.messages[2].CMD = true
	f.messages[2].UnsupportedFields = []string{"stream_id", "sync_once"}
	f.messages[3].StreamParent = true
	f.messages[3].UnsupportedFields = []string{"stream_id", "stream_no"}
	for i := 4; i < 7; i++ {
		f.messages[i].UnsupportedFields = []string{"stream_id"}
	}
	p := dedupeTestPlan(2)
	p.Messages = &MessagePolicy{KeepLatestDuplicates: true, ExcludeCMD: true, CompactSequences: true}
	w := dedupeTestWorkspace(t)
	var details bytes.Buffer
	report, err := PlanMessageDedupe(context.Background(), p, w, f, f, &details, nil)
	require.NoError(t, err)
	require.Equal(t, 5, report.Version)
	require.False(t, report.CutoverReady)
	require.Zero(t, report.Unresolved)
	for _, node := range report.Nodes {
		require.EqualValues(t, 5, node.Protocol.Retained)
		require.EqualValues(t, 4, node.Protocol.RetainedUnsupported)
		require.Equal(t, map[string]uint64{"stream_id": 4, "stream_no": 1}, node.Protocol.RetainedFields)
		require.EqualValues(t, 2, node.Protocol.OmittedUnsupported)
		require.Equal(t, map[string]uint64{"stream_id": 2, "expire": 1, "sync_once": 1}, node.Protocol.OmittedFields)
		require.Len(t, node.Protocol.Samples["stream_id"], 3)
		require.Len(t, node.Protocol.Samples["stream_no"], 1)
		require.EqualValues(t, 1, node.StreamParents)
		require.Zero(t, node.DroppedStreamParents)
	}
	var again bytes.Buffer
	rerun, err := PlanMessageDedupe(context.Background(), p, w, f, f, &again, nil)
	require.NoError(t, err)
	require.Equal(t, report, rerun)
	require.Equal(t, details.String(), again.String())
	for _, key := range []string{"workflow/PREPARED", "conversion/COMPLETE"} {
		_, found, err := w.Get(context.Background(), []byte(key))
		require.NoError(t, err)
		require.False(t, found)
	}
}

func TestDedupeStreamExclusionPrecedesDuplicatesAndCountsCMDOnce(t *testing.T) {
	f := &dedupeFixture{messages: []DedupeMessage{dedupeFact(1, 1, 10, "key"), dedupeFact(1, 2, 10, "key"), dedupeFact(1, 3, 30, "cmd"), dedupeFact(1, 4, 40, "other")}}
	f.messages[1].Stream = true
	f.messages[1].StreamParent = true
	f.messages[1].UnsupportedFields = []string{"stream_no"}
	f.messages[2].CMD = true
	f.messages[2].Stream = true
	p := dedupeTestPlan(1)
	p.Messages = &MessagePolicy{KeepLatestDuplicates: true, ExcludeCMD: true, ExcludeStreams: true, CompactSequences: true}
	var out bytes.Buffer
	r, err := PlanMessageDedupe(context.Background(), p, dedupeTestWorkspace(t), f, f, &out, nil)
	require.NoError(t, err)
	require.Zero(t, r.Unresolved)
	n := r.Nodes[0]
	require.EqualValues(t, 1, n.StreamDrops)
	require.EqualValues(t, 1, n.CMDDrops)
	require.EqualValues(t, 2, n.Dropped)
	require.Zero(t, n.MessageIDGroups)
	require.EqualValues(t, 2, n.Protocol.Retained)
	require.Zero(t, n.Protocol.RetainedUnsupported)
	require.EqualValues(t, 1, n.DroppedStreamParents)
	require.EqualValues(t, 1, n.ChangedSequences)
}
