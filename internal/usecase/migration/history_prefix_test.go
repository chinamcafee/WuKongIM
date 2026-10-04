package migration

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

type prefixFixtureDecoder struct{}

func (prefixFixtureDecoder) InspectChannelConfig(r Row) (v ChannelConfigEvidence, err error) {
	err = json.Unmarshal(r.Value, &v)
	return
}
func (prefixFixtureDecoder) InspectMessage(r Row, _ int) (v MessageEvidence, err error) {
	err = json.Unmarshal(r.Value, &v)
	return
}
func (prefixFixtureDecoder) DecodeHistoryConfigCommand(r RawConfigCommand) (v ChannelConfigLog) {
	_ = json.Unmarshal(r.Data, &v)
	return
}
func (prefixFixtureDecoder) HistoryLayout(owner uint64, shards int) (HistoryLayout, error) {
	return HistoryLayout{ConfigKey: []byte(fmt.Sprintf("config/%020d", owner)), MessageShard: int(owner % uint64(shards)), MessagePrefix: []byte(fmt.Sprintf("message/%020d/", owner)), TailKey: []byte(fmt.Sprintf("tail/%020d", owner))}, nil
}
func (prefixFixtureDecoder) HistoryMessageKey(owner, sequence uint64) []byte {
	return []byte(fmt.Sprintf("message/%020d/%020d", owner, sequence))
}

// prefixFixture stores original row references and complete message evidence.
// The baseline reproduces a formal follower with only a strict history prefix.
type prefixFixture struct {
	configs  map[uint64]ChannelConfigEvidence
	logs     map[uint64][]ChannelConfigLog
	messages map[uint64][]MessageEvidence
	tails    map[uint64]uint64
}

func newPrefixFixture() *prefixFixture {
	f := &prefixFixture{configs: map[uint64]ChannelConfigEvidence{}, logs: map[uint64][]ChannelConfigLog{}, messages: map[uint64][]MessageEvidence{}, tails: map[uint64]uint64{}}
	for n := uint64(1); n <= 3; n++ {
		c := ChannelConfigEvidence{Owner: 7, IdentitySHA256: diagnosticSHA([]byte(IdentityKey("channel", uint8(2)))), Leader: 1, Replicas: []uint64{1, 2, 3}, ReplicaMax: 3, Term: 1, Version: 10, SHA256: diagnosticSHA([]byte("config"))}
		f.configs[n] = c
		f.logs[n] = []ChannelConfigLog{{Index: 10, Term: 2, Config: c, CommandSHA256: diagnosticSHA([]byte("command"))}}
		for s := uint64(1); s <= 3; s++ {
			f.messages[n] = append(f.messages[n], MessageEvidence{ID: 100 + s, Sequence: s, Term: 1, SHA256: diagnosticSHA([]byte(fmt.Sprint(s)))})
		}
		f.tails[n] = 3
	}
	f.messages[3], f.tails[3] = f.messages[3][:1], 1
	return f
}

func (f *prefixFixture) capture(t *testing.T, w Workspace) SourceCapture {
	t.Helper()
	ctx := context.Background()
	capture := SourceCapture{Digest: diagnosticSHA([]byte("capture"))}
	d := prefixFixtureDecoder{}
	put := func(node uint64, row Row) {
		v, e := json.Marshal(row)
		require.NoError(t, e)
		require.NoError(t, w.Put(ctx, []transfer.SpoolRow{{Key: sourceRowKey(node, row), Value: v}}))
	}
	for n := uint64(1); n <= 3; n++ {
		p := LogProgress{Group: "0", FirstIndex: 1, LastIndex: 10, AppliedIndex: 10, LastTerm: 2, LastDigest: "last", LogDigest: "all"}
		capture.Nodes = append(capture.Nodes, NodeSnapshot{NodeID: n, ConfigProgress: p, SlotProgress: []LogProgress{p}, Config: SourceConfig{Version: 10, SlotCount: 1, Nodes: []SourceNode{{ID: 1}, {ID: 2}, {ID: 3}}, Slots: []SourceSlot{{ID: 0, Leader: 1, Replicas: []uint64{1, 2, 3}}}}})
		layout, e := d.HistoryLayout(7, 1)
		require.NoError(t, e)
		v, e := json.Marshal(f.configs[n])
		require.NoError(t, e)
		put(n, Row{Table: "ChannelClusterConfig", Kind: Primary, ID: 7, Key: layout.ConfigKey, Value: v})
		for _, m := range f.messages[n] {
			v, e := json.Marshal(m)
			require.NoError(t, e)
			put(n, Row{Table: "Message", Kind: Primary, Owner: 7, ID: m.Sequence, Key: d.HistoryMessageKey(7, m.Sequence), Value: v})
		}
		if tail, ok := f.tails[n]; ok {
			v := make([]byte, 16)
			binary.BigEndian.PutUint64(v, tail)
			put(n, Row{Table: "Message", Kind: Other, Key: layout.TailKey, Value: v})
		}
		for _, l := range f.logs[n] {
			data, e := json.Marshal(l)
			require.NoError(t, e)
			r := RawConfigCommand{Slot: l.Slot, Index: l.Index, Term: l.Term, Data: data}
			v, e := json.Marshal(r)
			require.NoError(t, e)
			require.NoError(t, w.Put(ctx, []transfer.SpoolRow{{Key: capturedCommandKey(n, r), Value: v}}))
		}
		count, digest, e := walkCapturedCommands(ctx, w, n, nil)
		require.NoError(t, e)
		capture.Authority = append(capture.Authority, CapturedAuthority{NodeID: n, ShardCount: 1, Commands: count, SHA256: digest})
	}
	return capture
}

func TestCapturedHistoryPrefixRequiresCompleteLeaderAndConfigEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		edit         func(*prefixFixture)
	}{
		{"leader_and_peer_complete", "", func(*prefixFixture) {}},
		{"duplicate_formal_member", "unsupported_channel_configuration", func(f *prefixFixture) {
			for n, c := range f.configs {
				c.Replicas = []uint64{1, 2, 2}
				f.configs[n] = c
			}
		}},
		{"zero_formal_member", "unsupported_channel_configuration", func(f *prefixFixture) {
			for n, c := range f.configs {
				c.Replicas = []uint64{1, 2, 0}
				f.configs[n] = c
			}
		}},
		{"empty_follower", "", func(f *prefixFixture) { f.messages[3] = nil; delete(f.tails, 3) }},
		{"only_leader_complete", "durable_history_lacks_formal_quorum", func(f *prefixFixture) { f.messages[2] = f.messages[2][:1]; f.tails[2] = 1 }},
		{"leader_empty", "configured_leader_is_not_complete", func(f *prefixFixture) {
			f.messages[3] = append([]MessageEvidence(nil), f.messages[2]...)
			f.tails[3] = 3
			f.messages[1] = nil
			delete(f.tails, 1)
		}},
		{"conflicting_payload", "history_is_not_an_exact_prefix", func(f *prefixFixture) { f.messages[3][0].SHA256 = diagnosticSHA([]byte("different")) }},
		{"middle_hole", "invalid_message_history", func(f *prefixFixture) {
			f.messages[3] = []MessageEvidence{f.messages[1][0], f.messages[1][2]}
			f.tails[3] = 3
		}},
		{"tail_ahead", "tail_does_not_match_history", func(f *prefixFixture) { f.tails[3] = 4 }},
		{"missing_tail", "tail_does_not_match_history", func(f *prefixFixture) { delete(f.tails, 1) }},
		{"future_message_term", "invalid_message_history", func(f *prefixFixture) { f.messages[1][2].Term = 2 }},
		{"backwards_message_term", "invalid_message_history", func(f *prefixFixture) { f.messages[1][1].Term = 0 }},
		{"missing_config_log", "retained_config_command_missing", func(f *prefixFixture) { f.logs[2] = nil }},
		{"config_fields_differ", "owner_slot_config_disagreement", func(f *prefixFixture) { c := f.configs[2]; c.SHA256 = diagnosticSHA([]byte("other")); f.configs[2] = c }},
		{"config_apply_mismatch", "stored_config_differs_from_last_applied_command", func(f *prefixFixture) { f.logs[2][0].Config.SHA256 = diagnosticSHA([]byte("other")) }},
		{"unapplied_config", "unapplied_config_change", func(f *prefixFixture) { l := f.logs[2][0]; l.Index = 11; f.logs[2] = append(f.logs[2], l) }},
		{"deleted_generation", "retained_channel_deletion", func(f *prefixFixture) {
			for n := range f.logs {
				l := f.logs[n][0]
				l.Index = 9
				l.Deleted = true
				f.logs[n] = append([]ChannelConfigLog{l}, f.logs[n]...)
			}
		}},
		{"same_term_leader_change", "leader_changed_without_new_term", func(f *prefixFixture) {
			for n := range f.logs {
				l := f.logs[n][0]
				l.Index = 9
				l.Config.Leader = 2
				f.logs[n] = append([]ChannelConfigLog{l}, f.logs[n]...)
			}
		}},
		{"membership_change", "retained_membership_change", func(f *prefixFixture) {
			for n := range f.logs {
				l := f.logs[n][0]
				l.Index = 9
				l.Config.Replicas = []uint64{1, 2}
				f.logs[n] = append([]ChannelConfigLog{l}, f.logs[n]...)
			}
		}},
		{"command_disagreement", "retained_config_commands_disagree", func(f *prefixFixture) { f.logs[2][0].CommandSHA256 = diagnosticSHA([]byte("different")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := dedupeTestWorkspace(t)
			f := newPrefixFixture()
			tc.edit(f)
			capture := f.capture(t, w)
			var reports []HistoryPrefixReport
			err := InspectCapturedHistoryPrefixes(context.Background(), capture, w, prefixFixtureDecoder{}, []uint64{7}, func(r HistoryPrefixReport) error { reports = append(reports, r); return nil })
			require.NoError(t, err)
			require.Len(t, reports, 1)
			r := reports[0]
			if tc.reason == "" {
				require.Equal(t, "leader_quorum_prefix", r.Class)
				require.Equal(t, uint64(1), r.CandidateNode)
				require.Empty(t, r.Reasons)
			} else {
				require.Equal(t, "unresolved", r.Class)
				require.Zero(t, r.CandidateNode)
				require.Contains(t, r.Reasons, tc.reason)
			}
			require.False(t, r.HistoricalACKProven)
			require.NotEmpty(t, r.Digest)
			_, ok, e := w.Get(context.Background(), []byte("workflow/PREPARED"))
			require.NoError(t, e)
			require.False(t, ok)
		})
	}
}
