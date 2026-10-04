package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// HistoryLayout describes original primary-row placement. Storage key encoding
// stays in the original-format adapter, separate from history recovery rules.
type HistoryLayout struct {
	ConfigShard   int
	ConfigKey     []byte
	MessageShard  int
	MessagePrefix []byte
	TailKey       []byte
}

type HistoryPrefixDecoder interface {
	AuthorityDecoder
	// DecodeHistoryConfigCommand identifies a complete original command,
	// including unrelated legacy administrative identities. It grants no
	// archival or import compatibility to the decoded channel.
	DecodeHistoryConfigCommand(RawConfigCommand) ChannelConfigLog
	HistoryLayout(owner uint64, shards int) (HistoryLayout, error)
	HistoryMessageKey(owner, sequence uint64) []byte
}

// HistoryPrefixConfig binds the entire retained command sequence, not only the
// latest config. A deletion or unexplained generation/leadership reset blocks.
type HistoryPrefixConfig struct {
	NodeID         uint64 `json:"node_id,string"`
	ConfigSHA256   string `json:"config_sha256"`
	Commands       uint64 `json:"commands"`
	CommandsSHA256 string `json:"commands_sha256"`
	VersionRule    string `json:"version_rule"`
}

// HistoryPrefixCopy counts complete original rows before any message omission
// or deduplication. MissingSuffix is a physical replica deficit, not data loss.
type HistoryPrefixCopy struct {
	NodeID          uint64 `json:"node_id,string"`
	Messages        uint64 `json:"messages"`
	Last            uint64 `json:"last,string"`
	LastTerm        uint64 `json:"last_term,string"`
	SHA256          string `json:"sha256"`
	TailPresent     bool   `json:"tail_present"`
	Tail            uint64 `json:"tail,string"`
	MissingSuffix   uint64 `json:"missing_suffix"`
	PrefixConflicts uint64 `json:"prefix_conflicts"`
}

// HistoryPrefixReport is a diagnostic of durable histories. It does not create
// selected rows, a PREPARED checkpoint, a client ACK proof, or cutover permission.
// CandidateNode can only be the current configured Leader, never a longest
// follower chosen to replace an empty or shorter Leader.
type HistoryPrefixReport struct {
	Version             int                   `json:"version"`
	CaptureDigest       string                `json:"capture_digest"`
	Owner               uint64                `json:"owner_hash,string"`
	IdentitySHA256      string                `json:"identity_sha256"`
	Slot                uint32                `json:"slot"`
	Leader              uint64                `json:"leader,string"`
	Replicas            []uint64              `json:"replicas"`
	CandidateNode       uint64                `json:"candidate_node,string"`
	CompleteNodes       []uint64              `json:"complete_nodes"`
	Configs             []HistoryPrefixConfig `json:"configs"`
	Histories           []HistoryPrefixCopy   `json:"histories"`
	Class               string                `json:"class"`
	Reasons             []string              `json:"reasons"`
	HistoricalACKProven bool                  `json:"historical_ack_proven"`
	Digest              string                `json:"digest"`
}

// InspectCapturedHistoryPrefixes rechecks bounded, explicitly named channels
// from raw capture. It never trusts earlier diagnostic reports or writes to the
// workspace. Every original command is rehashed once; messages are streamed with
// O(source nodes) summaries and one point lookup per compared row.
func InspectCapturedHistoryPrefixes(ctx context.Context, capture SourceCapture, w Workspace, decoder HistoryPrefixDecoder, owners []uint64, visit func(HistoryPrefixReport) error) error {
	if len(owners) == 0 || len(owners) > 1024 || visit == nil {
		return errors.New("history prefix inspection requires 1..1024 distinct owners and a visitor")
	}
	ordered := append([]uint64(nil), owners...)
	slices.Sort(ordered)
	for i := 1; i < len(ordered); i++ {
		if ordered[i] == ordered[i-1] {
			return errors.New("duplicate history prefix owner")
		}
	}
	a, err := newHistoryPrefixInspector(ctx, capture, w, decoder)
	if err != nil {
		return err
	}
	for _, owner := range ordered {
		r, err := a.channel(owner)
		if err != nil {
			return err
		}
		if err := visit(r); err != nil {
			return err
		}
	}
	return nil
}

type historyPrefixInspector struct {
	ctx     context.Context
	w       Workspace
	capture SourceCapture
	decoder HistoryPrefixDecoder
	nodes   []NodeSnapshot
	shards  map[uint64]int
}

func newHistoryPrefixInspector(ctx context.Context, capture SourceCapture, w Workspace, decoder HistoryPrefixDecoder) (*historyPrefixInspector, error) {
	if ctx == nil || w == nil || decoder == nil || capture.Digest == "" || len(capture.Nodes) > 1024 || len(capture.Authority) != len(capture.Nodes) {
		return nil, errors.New("history prefix inspection requires a complete original capture")
	}
	if err := validateCapturedAuthority(capture.Nodes); err != nil {
		return nil, err
	}
	if capture.Nodes[0].Config.SlotCount == 0 || uint32(len(capture.Nodes[0].Config.Slots)) != capture.Nodes[0].Config.SlotCount {
		return nil, errors.New("history prefix inspection requires complete source Slot ownership")
	}
	a := &historyPrefixInspector{ctx: ctx, w: w, capture: capture, decoder: decoder, nodes: append([]NodeSnapshot(nil), capture.Nodes...), shards: map[uint64]int{}}
	sort.Slice(a.nodes, func(i, j int) bool { return a.nodes[i].NodeID < a.nodes[j].NodeID })
	for _, evidence := range capture.Authority {
		known := slices.ContainsFunc(a.nodes, func(n NodeSnapshot) bool { return n.NodeID == evidence.NodeID })
		if !known || a.shards[evidence.NodeID] != 0 || evidence.ShardCount < 1 || evidence.ShardCount > 1024 {
			return nil, errors.New("invalid history prefix source inventory")
		}
		count, digest, err := walkCapturedCommands(ctx, w, evidence.NodeID, nil)
		if err != nil {
			return nil, err
		}
		if count != evidence.Commands || digest != evidence.SHA256 {
			return nil, errors.New("captured authority command digest mismatch")
		}
		a.shards[evidence.NodeID] = evidence.ShardCount
	}
	return a, nil
}

func (a *historyPrefixInspector) sourceRow(node uint64, shard int, key []byte) (row Row, found bool, err error) {
	k := sourceRowKey(node, Row{Shard: shard, Key: key})
	data, found, err := a.w.Get(a.ctx, k)
	if err != nil || !found {
		return row, found, err
	}
	if err = json.Unmarshal(data, &row); err != nil {
		return row, found, err
	}
	if !bytes.Equal(k, sourceRowKey(node, row)) {
		return row, found, errors.New("history prefix source row identity mismatch")
	}
	return row, found, nil
}

func (a *historyPrefixInspector) channel(owner uint64) (r HistoryPrefixReport, err error) {
	r = HistoryPrefixReport{Version: 1, CaptureDigest: a.capture.Digest, Owner: owner, Class: "unresolved"}
	reason := func(s string) {
		if !slices.Contains(r.Reasons, s) {
			r.Reasons = append(r.Reasons, s)
		}
	}
	configs := map[uint64]ChannelConfigEvidence{}
	layouts := map[uint64]HistoryLayout{}
	for _, n := range a.nodes {
		layout, e := a.decoder.HistoryLayout(owner, a.shards[n.NodeID])
		if e != nil {
			return r, e
		}
		layouts[n.NodeID] = layout
		row, found, e := a.sourceRow(n.NodeID, layout.ConfigShard, layout.ConfigKey)
		if e != nil {
			return r, e
		}
		if !found {
			continue
		}
		c, e := a.decoder.InspectChannelConfig(row)
		if e != nil {
			return r, e
		}
		if c.Owner != owner || c.IdentitySHA256 == "" || c.SHA256 == "" {
			return r, errors.New("history prefix config identity mismatch")
		}
		configs[n.NodeID] = c
	}
	if len(configs) == 0 {
		return r, errors.New("history prefix owner has no captured configuration")
	}
	var base ChannelConfigEvidence
	for _, n := range a.nodes {
		if c, ok := configs[n.NodeID]; ok {
			base = c
			break
		}
	}
	r.Slot = base.RoutingHash % a.nodes[0].Config.SlotCount
	var slot SourceSlot
	for _, s := range a.nodes[0].Config.Slots {
		if s.ID == r.Slot {
			slot = s
			break
		}
	}
	if c, ok := configs[slot.Leader]; ok {
		base = c
	} else {
		reason("owner_slot_leader_config_missing")
	}
	r.IdentitySHA256, r.Leader = base.IdentitySHA256, base.Leader
	r.Replicas = append([]uint64(nil), base.Replicas...)
	slices.Sort(r.Replicas)
	for _, n := range slot.Replicas {
		c, ok := configs[n]
		if !ok {
			reason("owner_slot_replica_config_missing")
			continue
		}
		if c.SHA256 != base.SHA256 {
			reason("owner_slot_config_disagreement")
		}
	}
	for _, c := range configs {
		if c.IdentitySHA256 != base.IdentitySHA256 || c.RoutingHash != base.RoutingHash {
			reason("channel_hash_identity_collision")
		}
	}
	if !stablePrefixConfig(base) {
		reason("unsupported_channel_configuration")
	}
	for _, n := range base.Replicas {
		if a.shards[n] == 0 {
			reason("configured_channel_member_missing")
		}
	}
	var logDigest string
	for _, n := range a.nodes {
		if !slices.Contains(slot.Replicas, n.NodeID) {
			continue
		}
		progress, e := capturedSlotProgress(n, r.Slot)
		if e != nil {
			return r, e
		}
		c := HistoryPrefixConfig{NodeID: n.NodeID, ConfigSHA256: configs[n.NodeID].SHA256}
		h := sha256.New()
		enc := json.NewEncoder(h)
		var previous, last *ChannelConfigLog
		e = a.w.Walk(a.ctx, []byte(fmt.Sprintf("source/%020d/config-commands/%010d/", n.NodeID, r.Slot)), func(item transfer.SpoolRow) error {
			var raw RawConfigCommand
			if e := json.Unmarshal(item.Value, &raw); e != nil {
				return e
			}
			if !bytes.Equal(item.Key, capturedCommandKey(n.NodeID, raw)) {
				return errors.New("history prefix config command placement mismatch")
			}
			l := a.decoder.DecodeHistoryConfigCommand(raw)
			if l.DecodeErrorSHA256 != "" {
				reason("config_log_decode_failure_in_owner_slot")
				return nil
			}
			if l.Config.Owner != owner {
				return nil
			}
			if l.Slot != r.Slot || l.Index != raw.Index || l.Term != raw.Term || l.Config.IdentitySHA256 != base.IdentitySHA256 || l.Config.RoutingHash != base.RoutingHash {
				reason("config_log_routing_or_identity_conflict")
			}
			c.Commands++
			if e := enc.Encode(raw); e != nil {
				return e
			}
			if l.Index > progress.AppliedIndex {
				reason("unapplied_config_change")
			}
			if l.Deleted {
				reason("retained_channel_deletion")
			}
			if !l.Deleted && !stablePrefixConfig(l.Config) {
				reason("unsupported_retained_configuration")
			}
			if previous != nil {
				if l.Index <= previous.Index || l.Term < previous.Term {
					reason("invalid_retained_command_order")
				}
				if l.Config.Term < previous.Config.Term {
					reason("channel_term_regressed")
				}
				if l.Config.Leader != previous.Config.Leader && l.Config.Term <= previous.Config.Term {
					reason("leader_changed_without_new_term")
				}
				if !samePrefixMembership(l.Config, previous.Config) {
					reason("retained_membership_change")
				}
			}
			copy := l
			previous = &copy
			if l.Index <= progress.AppliedIndex {
				last = &copy
			}
			return nil
		})
		if e != nil {
			return r, e
		}
		c.CommandsSHA256 = hex.EncodeToString(h.Sum(nil))
		if last == nil {
			reason("retained_config_command_missing")
		} else {
			c.VersionRule = originalConfigVersionRule(AuthorityConfigCopy{Config: configs[n.NodeID], LastApplied: last})
			if c.VersionRule == "" {
				reason("stored_config_differs_from_last_applied_command")
			}
		}
		if logDigest == "" {
			logDigest = c.CommandsSHA256
		} else if logDigest != c.CommandsSHA256 {
			reason("retained_config_commands_disagree")
		}
		r.Configs = append(r.Configs, c)
	}
	var reference uint64
	var maximum uint64
	for _, n := range a.nodes {
		layout := layouts[n.NodeID]
		h := HistoryPrefixCopy{NodeID: n.NodeID}
		digest := sha256.New()
		enc := json.NewEncoder(digest)
		e := a.walkMessages(n.NodeID, owner, layout, func(m MessageEvidence) error {
			if m.Invalid || m.Sequence == 0 || m.Sequence != h.Last+1 || m.Term == 0 || m.Term < h.LastTerm || m.Term > uint64(base.Term) || m.SHA256 == "" {
				reason("invalid_message_history")
			}
			h.Messages++
			h.Last, h.LastTerm = m.Sequence, m.Term
			return enc.Encode(m)
		})
		if e != nil {
			return r, e
		}
		h.SHA256 = hex.EncodeToString(digest.Sum(nil))
		tail, found, e := a.sourceRow(n.NodeID, layout.MessageShard, layout.TailKey)
		if e != nil {
			return r, e
		}
		h.TailPresent = found
		if found {
			if tail.Table != "Message" || tail.Kind != Other || len(tail.Value) != 16 {
				reason("invalid_tail_record")
			} else {
				h.Tail = binary.BigEndian.Uint64(tail.Value)
			}
		}
		if h.Tail != h.Last || (h.Messages > 0 && !found) {
			reason("tail_does_not_match_history")
		}
		if !slices.Contains(base.Replicas, n.NodeID) && (h.Messages > 0 || h.Tail > 0) {
			reason("history_outside_current_membership")
		}
		if slices.Contains(base.Replicas, n.NodeID) && (reference == 0 || h.Messages > maximum) {
			reference, maximum = n.NodeID, h.Messages
		}
		r.Histories = append(r.Histories, h)
	}
	if reference == 0 {
		return r, errors.New("history prefix has no captured formal replica")
	}
	var referenceHistory HistoryPrefixCopy
	for _, h := range r.Histories {
		if h.NodeID == reference {
			referenceHistory = h
		}
	}
	for i := range r.Histories {
		h := &r.Histories[i]
		if h.Messages <= maximum {
			h.MissingSuffix = maximum - h.Messages
		}
		if h.NodeID != reference {
			e := a.walkMessages(h.NodeID, owner, layouts[h.NodeID], func(m MessageEvidence) error {
				row, found, e := a.sourceRow(reference, layouts[reference].MessageShard, a.decoder.HistoryMessageKey(owner, m.Sequence))
				if e != nil {
					return e
				}
				if !found {
					h.PrefixConflicts++
					return nil
				}
				ref, e := a.decoder.InspectMessage(row, a.shards[reference])
				if e != nil {
					return e
				}
				if ref != m {
					h.PrefixConflicts++
				}
				return nil
			})
			if e != nil {
				return r, e
			}
		}
		if h.PrefixConflicts > 0 {
			reason("history_is_not_an_exact_prefix")
		}
		if slices.Contains(base.Replicas, h.NodeID) && h.Messages == maximum && h.SHA256 == referenceHistory.SHA256 {
			r.CompleteNodes = append(r.CompleteNodes, h.NodeID)
		}
	}
	if len(r.CompleteNodes) < int(base.ReplicaMax)/2+1 {
		reason("durable_history_lacks_formal_quorum")
	}
	if !slices.Contains(r.CompleteNodes, base.Leader) {
		reason("configured_leader_is_not_complete")
	}
	if len(r.Reasons) == 0 {
		r.Class = "leader_quorum_prefix"
		r.CandidateNode = base.Leader
	}
	sort.Strings(r.Reasons)
	data, e := json.Marshal(r)
	if e != nil {
		return r, e
	}
	r.Digest = diagnosticSHA(data)
	return r, nil
}

func (a *historyPrefixInspector) walkMessages(node, owner uint64, layout HistoryLayout, visit func(MessageEvidence) error) error {
	prefix := sourceRowKey(node, Row{Shard: layout.MessageShard, Key: layout.MessagePrefix})
	return a.w.Walk(a.ctx, prefix, func(item transfer.SpoolRow) error {
		var row Row
		if e := json.Unmarshal(item.Value, &row); e != nil {
			return e
		}
		if !bytes.Equal(item.Key, sourceRowKey(node, row)) || row.Table != "Message" || row.Kind != Primary || row.Owner != owner {
			return errors.New("history prefix primary row placement mismatch")
		}
		m, e := a.decoder.InspectMessage(row, a.shards[node])
		if e != nil {
			return e
		}
		if !bytes.Equal(row.Key, a.decoder.HistoryMessageKey(owner, m.Sequence)) {
			return errors.New("history prefix sequence/key mismatch")
		}
		return visit(m)
	})
}

func stablePrefixConfig(c ChannelConfigEvidence) bool {
	seen := make(map[uint64]bool, len(c.Replicas))
	for _, node := range c.Replicas {
		if node == 0 || seen[node] {
			return false
		}
		seen[node] = true
	}
	return c.Leader != 0 && c.Term > 0 && c.ReplicaMax > 0 && len(c.Replicas) == int(c.ReplicaMax) && len(c.Replicas) <= 1024 && len(c.Learners) == 0 && c.MigrateFrom == 0 && c.MigrateTo == 0 && c.Status == 0 && slices.Contains(c.Replicas, c.Leader)
}

func samePrefixMembership(a, b ChannelConfigEvidence) bool {
	x, y := append([]uint64(nil), a.Replicas...), append([]uint64(nil), b.Replicas...)
	slices.Sort(x)
	slices.Sort(y)
	return a.ReplicaMax == b.ReplicaMax && slices.Equal(x, y)
}
