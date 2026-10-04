package migration

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

type authorityFixture struct {
	config                                                  ChannelConfigEvidence
	messages                                                map[uint64][]MessageEvidence
	missingLog, conflictingConfig, missingNode, changedNode uint64
	appendLog                                               bool
	calls                                                   map[uint64]int
	logs                                                    map[uint64][]ChannelConfigLog
}

func newAuthorityFixture() *authorityFixture {
	f := &authorityFixture{config: ChannelConfigEvidence{Owner: 7, IdentitySHA256: "identity", Leader: 1, Replicas: []uint64{1, 2}, Learners: []uint64{3}, ReplicaMax: 3, Term: 2, Version: 10, MigrateFrom: 3, MigrateTo: 3, SHA256: "config"}, messages: map[uint64][]MessageEvidence{}, calls: map[uint64]int{}}
	for n := uint64(1); n <= 3; n++ {
		for s := uint64(1); s <= 3; s++ {
			f.messages[n] = append(f.messages[n], MessageEvidence{ID: 100 + s, Sequence: s, Term: 2, SHA256: fmt.Sprint(s)})
		}
	}
	return f
}

func (f *authorityFixture) ReadAuthorityNode(ctx context.Context, n NodeOptions, rows func(Row) error, files func(SourceFile) error, logs func(ChannelConfigLog) error) (NodeSnapshot, error) {
	if n.NodeID == f.missingNode {
		return NodeSnapshot{}, fmt.Errorf("missing source")
	}
	f.calls[n.NodeID]++
	file := SourceFile{Path: "source", Size: 1, SHA256: "immutable"}
	var encoded bytes.Buffer
	_ = json.NewEncoder(&encoded).Encode(file)
	if files != nil {
		if err := files(file); err != nil {
			return NodeSnapshot{}, err
		}
	}
	if err := rows(Row{Table: "ChannelClusterConfig", Kind: Primary, ID: 7, Fields: map[string][]byte{"MigrateFrom": {3}, "node": {byte(n.NodeID)}}}); err != nil {
		return NodeSnapshot{}, err
	}
	for _, m := range f.messages[n.NodeID] {
		data, _ := json.Marshal(m)
		if err := rows(Row{Table: "Message", Kind: Primary, Owner: 7, ID: m.Sequence, Value: data}); err != nil {
			return NodeSnapshot{}, err
		}
	}
	if len(f.messages[n.NodeID]) > 0 {
		key, value := make([]byte, 12), make([]byte, 16)
		binary.BigEndian.PutUint64(key[4:], 7)
		binary.BigEndian.PutUint64(value, f.messages[n.NodeID][len(f.messages[n.NodeID])-1].Sequence)
		if err := rows(Row{Table: "Message", Kind: Other, Key: key, Value: value}); err != nil {
			return NodeSnapshot{}, err
		}
	}
	if f.logs != nil {
		for _, log := range f.logs[n.NodeID] {
			if err := logs(log); err != nil {
				return NodeSnapshot{}, err
			}
		}
	} else if n.NodeID != f.missingLog {
		if err := logs(ChannelConfigLog{Index: 10, Term: 2, Config: f.config, CommandSHA256: "command"}); err != nil {
			return NodeSnapshot{}, err
		}
		if f.appendLog {
			c := f.config
			c.Version = 11
			c.SHA256 = "later"
			if err := logs(ChannelConfigLog{Index: 11, Term: 2, Config: c}); err != nil {
				return NodeSnapshot{}, err
			}
		}
	}
	p := LogProgress{Group: "0", FirstIndex: 1, LastIndex: 10, AppliedIndex: 10, LastTerm: 2, LastDigest: "last", LogDigest: "all"}
	s := NodeSnapshot{NodeID: n.NodeID, DataDigest: diagnosticSHA(encoded.Bytes()), SlotProgress: []LogProgress{p}, ConfigProgress: p, Config: SourceConfig{Version: 10, SlotCount: 1, Nodes: []SourceNode{{ID: 1}, {ID: 2}, {ID: 3}}, Slots: []SourceSlot{{ID: 0, Leader: 1, Replicas: []uint64{1, 2, 3}}}}}
	if n.NodeID == f.changedNode && f.calls[n.NodeID] == 2 {
		s.DataDigest = "changed"
	}
	return s, nil
}

func (f *authorityFixture) InspectChannelConfig(r Row) (ChannelConfigEvidence, error) {
	c := f.config
	if uint64(r.Fields["node"][0]) == f.conflictingConfig {
		c.SHA256 = "conflicting"
	}
	return c, nil
}
func (*authorityFixture) InspectMessage(r Row, _ int) (m MessageEvidence, err error) {
	err = json.Unmarshal(r.Value, &m)
	return
}

func TestAuthorityClassifiesWithoutChoosingLargestOrMajority(t *testing.T) {
	for _, tc := range []struct {
		name, class, reason string
		edit                func(*authorityFixture)
	}{
		{"matching", "consistent_formal_replicas", "", func(*authorityFixture) {}},
		{"learner_prefix", "learner_lag_only", "", func(f *authorityFixture) { f.messages[3] = f.messages[3][:1] }},
		{"formal_lag", "insufficient_evidence", "formal_replicas_not_identical", func(f *authorityFixture) { f.messages[2] = f.messages[2][:1] }},
		{"same_seq_conflict", "conflict", "conflicting_or_unproven_extra_messages", func(f *authorityFixture) { f.messages[3][1].SHA256 = "changed" }},
		{"extra_learner_tail", "conflict", "conflicting_or_unproven_extra_messages", func(f *authorityFixture) {
			f.messages[3] = append(f.messages[3], MessageEvidence{ID: 104, Sequence: 4, Term: 2, SHA256: "4"})
		}},
		{"missing_log", "insufficient_evidence", "retained_config_command_missing", func(f *authorityFixture) { f.missingLog = 2 }},
		{"config_conflict", "conflict", "owner_slot_config_disagreement", func(f *authorityFixture) { f.conflictingConfig = 2 }},
		{"pending_config", "insufficient_evidence", "unapplied_config_changes", func(f *authorityFixture) { f.appendLog = true }},
		{"missing_node", "insufficient_evidence", "incomplete_source_inventory", func(f *authorityFixture) { f.missingNode = 2 }},
		{"changed_source", "insufficient_evidence", "incomplete_source_inventory", func(f *authorityFixture) { f.changedNode = 2 }},
		{"learner_hole", "insufficient_evidence", "invalid_or_incomplete_message_history", func(f *authorityFixture) { f.messages[3] = append(f.messages[3][:1], f.messages[3][2]) }},
		{"leader_transfer", "insufficient_evidence", "leadership_or_replica_replacement_requires_transition_proof", func(f *authorityFixture) { f.config.MigrateFrom = 1 }},
		{"same_node_formal_marker", "insufficient_evidence", "unsupported_or_ambiguous_transition", func(f *authorityFixture) { f.config.MigrateFrom = 1; f.config.MigrateTo = 1; f.config.Learners = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthorityFixture()
			tc.edit(f)
			plan := Plan{Sources: []NodeOptions{{NodeID: 1, Options: Options{ShardCount: 1}}, {NodeID: 2, Options: Options{ShardCount: 1}}, {NodeID: 3, Options: Options{ShardCount: 1}}}}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "audit", 128<<20)
			require.NoError(t, err)
			defer w.Close()
			var details bytes.Buffer
			r, err := AuditSourceAuthority(context.Background(), plan, w, f, f, &details, nil)
			require.NoError(t, err)
			require.Equal(t, uint64(1), r.Channels)
			require.Equal(t, uint64(1), r.Classes[tc.class])
			require.False(t, r.CutoverReady)
			require.Equal(t, diagnosticSHA(details.Bytes()), r.DetailsSHA256)
			d := json.NewDecoder(&details)
			var c AuthorityChannel
			for {
				var raw json.RawMessage
				err := d.Decode(&raw)
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				var typ struct{ Type string }
				require.NoError(t, json.Unmarshal(raw, &typ))
				if typ.Type == "channel" {
					require.NoError(t, json.Unmarshal(raw, &c))
				}
			}
			if tc.reason != "" {
				require.Contains(t, c.Reasons, tc.reason)
				require.Zero(t, c.CandidateNode)
			} else {
				require.Equal(t, uint64(1), c.CandidateNode)
			}
			for _, seal := range []string{"workflow/PREPARED", "conversion/COMPLETE"} {
				_, found, err := w.Get(context.Background(), []byte(seal))
				require.NoError(t, err)
				require.False(t, found)
			}
		})
	}
}
