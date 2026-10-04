package migration

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"slices"
	"sort"
)

func (a *authorityAudit) channel(owner uint64) (c AuthorityChannel, err error) {
	c = AuthorityChannel{Type: "channel", Owner: owner, Class: "insufficient_evidence", MigrationKind: "unknown"}
	reason := func(s string) {
		if !slices.Contains(c.Reasons, s) {
			c.Reasons = append(c.Reasons, s)
		}
	}
	copies := map[uint64]*AuthorityConfigCopy{}
	err = a.walk(fmt.Sprintf("authority/config/%020d/", owner), func(data []byte) error {
		var r configCopy
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		if _, found := copies[r.NodeID]; found {
			reason("duplicate_config_primary")
			return nil
		}
		if r.Invalid {
			reason("invalid_config_primary")
		}
		copies[r.NodeID] = &AuthorityConfigCopy{NodeID: r.NodeID, Config: r.Config}
		return nil
	})
	if err != nil {
		return c, err
	}
	if !a.report.ScanComplete {
		reason("incomplete_source_inventory")
	}
	if !a.report.TopologyChecked {
		reason("cluster_or_slot_progress_unresolved")
	}
	var seed *AuthorityConfigCopy
	for _, n := range a.report.Nodes {
		if seed == nil && copies[n.NodeID] != nil {
			seed = copies[n.NodeID]
		}
	}
	if seed == nil {
		return c, errors.New("marked owner lacks captured config")
	}
	var slot SourceSlot
	for _, n := range a.report.Nodes {
		if n.Complete && n.Snapshot.Config.SlotCount > 0 {
			c.Slot = seed.Config.RoutingHash % n.Snapshot.Config.SlotCount
			for _, s := range n.Snapshot.Config.Slots {
				if s.ID == c.Slot {
					slot = s
					break
				}
			}
			break
		}
	}
	base := copies[slot.Leader]
	if a.badSlots[c.Slot] {
		reason("config_log_decode_failure_in_owner_slot")
	}
	if base == nil {
		reason("owner_slot_leader_config_missing")
		base = seed
	}
	c.ReferenceNode = base.Config.Leader
	if _, ok := a.nodes[c.ReferenceNode]; !ok {
		reason("channel_leader_source_missing")
		for _, n := range a.report.Nodes {
			if n.Complete {
				c.ReferenceNode = n.NodeID
				break
			}
		}
	}
	for _, n := range slot.Replicas {
		copy := copies[n]
		if copy == nil {
			reason("owner_slot_replica_config_missing")
			continue
		}
		if copy.Config.IdentitySHA256 != base.Config.IdentitySHA256 {
			reason("channel_hash_identity_collision")
		}
		if copy.Config.SHA256 != base.Config.SHA256 {
			reason("owner_slot_config_disagreement")
		}
	}
	for _, n := range a.report.Nodes {
		copy := copies[n.NodeID]
		if copy == nil {
			copy = &AuthorityConfigCopy{NodeID: n.NodeID}
		}
		err = a.walk(fmt.Sprintf("authority/log/%020d/%020d/", owner, n.NodeID), func(data []byte) error {
			var e ChannelConfigLog
			if err := json.Unmarshal(data, &e); err != nil {
				return err
			}
			copy.RetainedConfigLogs++
			if e.Slot != c.Slot || e.Config.IdentitySHA256 != base.Config.IdentitySHA256 {
				reason("config_log_routing_or_identity_conflict")
			}
			p, err := capturedSlotProgress(n.Snapshot, e.Slot)
			if err != nil {
				reason("config_log_applied_progress_missing")
			} else if e.Index <= p.AppliedIndex {
				if copy.LastApplied == nil || copy.LastApplied.Index < e.Index {
					copy.PreviousApplied = copy.LastApplied
					v := e
					copy.LastApplied = &v
				} else if e.Index < copy.LastApplied.Index && (copy.PreviousApplied == nil || copy.PreviousApplied.Index < e.Index) {
					v := e
					copy.PreviousApplied = &v
				}
			} else {
				copy.UnappliedChanges++
			}
			return a.out.Encode(map[string]any{"type": "config_log", "node_id": fmt.Sprint(n.NodeID), "log": e})
		})
		if err != nil {
			return c, err
		}
		if slices.Contains(slot.Replicas, n.NodeID) {
			if copy.LastApplied == nil {
				reason("retained_config_command_missing")
			} else if copy.VersionRule = originalConfigVersionRule(*copy); copy.VersionRule == "" {
				reason("stored_config_differs_from_last_applied_command")
			}
			if copy.UnappliedChanges > 0 {
				reason("unapplied_config_changes")
			}
		}
		c.ConfigCopies = append(c.ConfigCopies, *copy)
	}
	c.MigrationKind = classifyMigrationKind(base.Config)
	if c.MigrationKind == "same_node_formal_marker" && provesHistoricalSelfLeaderMarker(c.ConfigCopies, slot.Replicas) {
		c.MigrationKind = "historical_self_leader_noop"
	}
	if c.MigrationKind == "unknown" || c.MigrationKind == "same_node_formal_marker" || base.Config.Status != 0 {
		reason("unsupported_or_ambiguous_transition")
	}
	if err := a.histories(&c); err != nil {
		return c, err
	}
	formalEqual, learnerLag, divergence, invalid := true, false, false, false
	for _, h := range c.Histories {
		formal := slices.Contains(base.Config.Replicas, h.NodeID)
		learner := slices.Contains(base.Config.Learners, h.NodeID)
		bad := h.Invalid > 0 || h.DuplicateSequences > 0 || h.Gaps > 0 || (h.Messages > 0 && (h.First != 1 || h.TailRecords != 1 || h.DurableTail != h.Last)) || (h.Messages == 0 && (h.DurableTail != 0 || h.TailRecords > 1))
		invalid = invalid || bad
		if formal && (bad || h.MissingFromReference > 0 || h.OnlyOnNode > 0 || h.Conflicts > 0) {
			formalEqual = false
		}
		if learner && h.MissingFromReference > 0 {
			learnerLag = true
		}
		if h.Conflicts > 0 || h.OnlyOnNode > 0 {
			divergence = true
		}
		if !formal && !learner && h.Messages > 0 {
			reason("history_outside_current_membership")
		}
	}
	for _, n := range append(append([]uint64{}, base.Config.Replicas...), base.Config.Learners...) {
		if _, exists := a.nodes[n]; !exists {
			reason("configured_channel_member_missing")
			formalEqual = false
		}
	}
	if invalid {
		reason("invalid_or_incomplete_message_history")
	}
	if !formalEqual {
		reason("formal_replicas_not_identical")
	}
	if divergence {
		reason("conflicting_or_unproven_extra_messages")
	}
	if slices.Contains(c.Reasons, "owner_slot_config_disagreement") || slices.Contains(c.Reasons, "config_log_routing_or_identity_conflict") || slices.Contains(c.Reasons, "channel_hash_identity_collision") || divergence {
		c.Class = "conflict"
	} else if len(c.Reasons) == 0 && formalEqual {
		if c.MigrationKind == "add_replica" || c.MigrationKind == "none" || c.MigrationKind == "historical_self_leader_noop" {
			c.Class = "consistent_formal_replicas"
			if learnerLag {
				c.Class = "learner_lag_only"
			}
			c.CandidateNode = base.Config.Leader
		} else {
			reason("leadership_or_replica_replacement_requires_transition_proof")
		}
	}
	sort.Strings(c.Reasons)
	return c, nil
}

// originalConfigVersionRule checks both original v2 apply implementations.
// The 2024 clusterstore persisted encoded ConfVersion; cluster2 began using
// log.Index in aef7ff71c. Both require all original fields to match exactly.
func originalConfigVersionRule(c AuthorityConfigCopy) string {
	l := c.LastApplied
	if l == nil || l.Deleted || c.Config.SHA256 == "" {
		return ""
	}
	if l.Index == c.Config.Version && l.Config.SHA256 == c.Config.SHA256 {
		return "slot_log_index"
	}
	if l.EncodedVersion == c.Config.Version && l.EncodedConfigSHA256 == c.Config.SHA256 {
		return "original_encoded_payload"
	}
	return ""
}

// provesHistoricalSelfLeaderMarker recognizes the old channelMigrate API's
// leader-to-itself request. At 9b699e31a it changed only the markers and version;
// no learner was added and remote sync could not complete a self transfer.
// Require that exact predecessor on every owner-Slot replica. Other same-node
// markers remain unresolved. Message agreement is checked separately.
func provesHistoricalSelfLeaderMarker(copies []AuthorityConfigCopy, replicas []uint64) bool {
	if len(replicas) == 0 {
		return false
	}
	var previousSHA, lastSHA string
	for _, n := range replicas {
		var copy *AuthorityConfigCopy
		for i := range copies {
			if copies[i].NodeID == n {
				copy = &copies[i]
				break
			}
		}
		if copy == nil || copy.VersionRule != "original_encoded_payload" || copy.UnappliedChanges != 0 || copy.PreviousApplied == nil || copy.LastApplied == nil {
			return false
		}
		c, p, l := copy.Config, copy.PreviousApplied, copy.LastApplied
		if c.MigrateFrom != c.Leader || c.MigrateTo != c.Leader || c.Leader == 0 || c.Status != 0 || len(c.Learners) != 0 || len(c.Replicas) != int(c.ReplicaMax) {
			return false
		}
		if p.Deleted || p.Index >= l.Index || p.Term > l.Term || p.Config.MigrateFrom != 0 || p.Config.MigrateTo != 0 || p.Config.NonMigrationSHA256 == "" || p.Config.NonMigrationSHA256 != c.NonMigrationSHA256 || l.Config.NonMigrationSHA256 != c.NonMigrationSHA256 {
			return false
		}
		if p.CommandSHA256 == "" || l.CommandSHA256 == "" {
			return false
		}
		if previousSHA == "" {
			previousSHA, lastSHA = p.CommandSHA256, l.CommandSHA256
		} else if previousSHA != p.CommandSHA256 || lastSHA != l.CommandSHA256 {
			return false
		}
	}
	return true
}

func classifyMigrationKind(c ChannelConfigEvidence) string {
	if c.MigrateFrom == 0 && c.MigrateTo == 0 && len(c.Learners) == 0 {
		return "none"
	}
	if c.MigrateFrom == 0 || c.MigrateTo == 0 {
		return "unknown"
	}
	if c.MigrateFrom == c.MigrateTo && slices.Contains(c.Learners, c.MigrateTo) && !slices.Contains(c.Replicas, c.MigrateTo) {
		return "add_replica"
	}
	if c.MigrateFrom == c.MigrateTo && slices.Contains(c.Replicas, c.MigrateTo) {
		return "same_node_formal_marker"
	}
	if c.MigrateFrom == c.Leader && (slices.Contains(c.Replicas, c.MigrateTo) || slices.Contains(c.Learners, c.MigrateTo)) {
		return "leader_transfer"
	}
	if slices.Contains(c.Replicas, c.MigrateFrom) && slices.Contains(c.Learners, c.MigrateTo) {
		return "replace_follower"
	}
	return "unknown"
}

// histories joins one sequence at a time. Memory is bounded by source nodes;
// every divergent sequence is emitted, while channel summaries retain counts.
func (a *authorityAudit) histories(c *AuthorityChannel) error {
	stats := map[uint64]*AuthorityHistory{}
	hashes := map[uint64]hash.Hash{}
	for _, n := range a.report.Nodes {
		stats[n.NodeID] = &AuthorityHistory{NodeID: n.NodeID}
		hashes[n.NodeID] = sha256.New()
	}
	var seq uint64
	group := map[uint64]messageCopy{}
	flush := func() error {
		if len(group) == 0 {
			return nil
		}
		ref, present := group[c.ReferenceNode]
		different := false
		var rows []messageCopy
		for _, n := range a.report.Nodes {
			h := stats[n.NodeID]
			m, ok := group[n.NodeID]
			if ok {
				rows = append(rows, m)
			}
			if n.NodeID == c.ReferenceNode {
				continue
			}
			if present && !ok {
				h.MissingFromReference++
				different = true
			}
			if !present && ok {
				h.OnlyOnNode++
				different = true
			}
			if present && ok && ref.SHA256 != m.SHA256 {
				h.Conflicts++
				different = true
			}
		}
		if different {
			return a.out.Encode(map[string]any{"type": "message_difference", "owner_hash": fmt.Sprint(c.Owner), "sequence": fmt.Sprint(seq), "reference_node": fmt.Sprint(c.ReferenceNode), "copies": rows})
		}
		return nil
	}
	err := a.walk(fmt.Sprintf("authority/message/%020d/", c.Owner), func(data []byte) error {
		var m messageCopy
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		if seq != m.Sequence {
			if err := flush(); err != nil {
				return err
			}
			seq = m.Sequence
			clear(group)
		}
		h := stats[m.NodeID]
		if h == nil {
			return errors.New("message source outside audit plan")
		}
		h.Messages++
		if m.Invalid {
			h.Invalid++
		}
		if _, duplicate := group[m.NodeID]; duplicate {
			h.DuplicateSequences++
			if err := a.out.Encode(map[string]any{"type": "duplicate_sequence", "owner_hash": fmt.Sprint(c.Owner), "copies": []messageCopy{group[m.NodeID], m}}); err != nil {
				return err
			}
		} else {
			if h.Messages == 1 {
				h.First = m.Sequence
			} else if m.Sequence > h.Last+1 {
				h.Gaps += m.Sequence - h.Last - 1
			}
			h.Last = m.Sequence
			group[m.NodeID] = m
		}
		_, err := fmt.Fprintf(hashes[m.NodeID], "%020d:%s\n", m.Sequence, m.SHA256)
		return err
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	for _, n := range a.report.Nodes {
		h := stats[n.NodeID]
		h.SHA256 = hex.EncodeToString(hashes[n.NodeID].Sum(nil))
		err := a.walk(fmt.Sprintf("authority/tail/%020d/%020d/", c.Owner, n.NodeID), func(data []byte) error {
			var tail authorityTail
			if err := json.Unmarshal(data, &tail); err != nil {
				return err
			}
			h.TailRecords++
			if tail.Invalid || len(tail.Value) != 16 {
				h.Invalid++
			} else {
				h.DurableTail = binary.BigEndian.Uint64(tail.Value)
			}
			return nil
		})
		if err != nil {
			return err
		}
		c.Histories = append(c.Histories, *h)
	}
	return nil
}
