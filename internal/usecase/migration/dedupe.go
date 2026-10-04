package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

type dedupePlanner struct {
	excludeCMD     bool
	excludeStreams bool
	ctx            context.Context
	w              Workspace
	b              *captureBatch
	out            *json.Encoder
	report         DedupeReport
}

// PlanMessageDedupe produces source-bound keep/drop candidates with bounded
// memory. Disk-sorted identity groups and point joins never retain whole message
// histories or payloads. Replica copies are evaluated independently; authority
// selection must precede applying any future conversion policy.
func PlanMessageDedupe(ctx context.Context, plan Plan, w Workspace, source Source, decoder DedupeDecoder, details io.Writer, progress func(uint64, string)) (DedupeReport, error) {
	if ctx == nil || w == nil || source == nil || decoder == nil || details == nil || len(plan.Sources) == 0 || len(plan.Sources) > 1024 {
		return DedupeReport{}, errors.New("dedupe plan requires complete source inventory, workspace and details writer")
	}
	h := sha256.New()
	p := &dedupePlanner{ctx: ctx, w: w, b: &captureBatch{ctx: ctx, workspace: w}, out: json.NewEncoder(io.MultiWriter(details, h))}
	p.excludeCMD = plan.Messages != nil && plan.Messages.ExcludeCMD
	p.excludeStreams = plan.Messages != nil && plan.Messages.ExcludeStreams
	if err := validateMessagePolicy(plan.Messages); err != nil {
		return p.report, err
	}
	p.report = DedupeReport{Version: 5, Status: "planned", PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit,
		Rule:         "greatest original MessageSeq within a channel after any explicit CMD and stream exclusions (CMD reason takes precedence); MessageID or (channel, sender, nonempty ClientMsgNo); empty sender/client key exempt; cross-channel IDs and superseded winners unresolved",
		NotCertified: []string{"authoritative source selection", "sequence renumbering and read/delete/CMD cursor mapping", "business conversion, target installation, API behavior and cutover"}}
	opts := append([]NodeOptions(nil), plan.Sources...)
	sort.Slice(opts, func(i, j int) bool { return opts[i].NodeID < opts[j].NodeID })
	for i, n := range opts {
		if n.NodeID == 0 || (i > 0 && opts[i-1].NodeID == n.NodeID) {
			return p.report, errors.New("duplicate or zero source node")
		}
		if progress != nil {
			progress(n.NodeID, "dedupe: reading immutable stopped-source message identities")
		}
		node := DedupeNode{NodeID: n.NodeID, Protocol: DedupeProtocolImpact{
			RetainedFields: map[string]uint64{}, OmittedFields: map[string]uint64{}, Samples: map[string][]DedupeMessage{},
		}}
		files := sha256.New()
		enc := json.NewEncoder(files)
		sealed := false
		seal := func() error {
			if sealed {
				return nil
			}
			sealed = true
			if err := p.put(fmt.Sprintf("dedupe/source/%020d/inventory", n.NodeID), hex.EncodeToString(files.Sum(nil))); err != nil {
				return err
			}
			return p.b.flush()
		}
		snapshot, err := source.ReadStoppedNode(ctx, n, func(row Row) error {
			if err := seal(); err != nil {
				return err
			}
			if row.Table != "Message" || row.Kind != Primary {
				return nil
			}
			m, err := decoder.InspectDedupeMessage(row, n.ShardCount)
			if err != nil {
				return fmt.Errorf("invalid source message node=%d owner=%d seq=%d: %w", n.NodeID, row.Owner, row.ID, err)
			}
			if m.ID == 0 || m.Sequence == 0 || m.Invalid || m.Owner != row.Owner || m.Sequence != row.ID || m.ChannelSHA256 == "" || m.SHA256 == "" {
				return errors.New("invalid dedupe message identity")
			}
			m.NodeID = n.NodeID
			node.Messages++
			if m.StreamParent {
				node.StreamParents++
			}
			if err := p.put(dedupeMessageKey("message", m), m); err != nil {
				return err
			}
			if (p.excludeCMD && m.CMD) || (p.excludeStreams && m.Stream) || (plan.Messages != nil && !plan.Messages.KeepLatestDuplicates) {
				return nil
			}
			if err := p.put(dedupeGroupKey("id", m)+fmt.Sprintf("/%020d/%020d", m.Owner, m.Sequence), m); err != nil {
				return err
			}
			if m.ClientKeySHA256 != "" {
				return p.put(dedupeGroupKey("client", m)+fmt.Sprintf("/%020d/%020d", m.Owner, m.Sequence), m)
			}
			return nil
		}, func(f SourceFile) error {
			if sealed {
				return errors.New("dedupe file inventory after rows")
			}
			return enc.Encode(f)
		})
		if err != nil {
			return p.report, err
		}
		if err := seal(); err != nil {
			return p.report, err
		}
		if snapshot.NodeID != n.NodeID || snapshot.DataDigest != hex.EncodeToString(files.Sum(nil)) {
			return p.report, errors.New("dedupe source snapshot differs from file inventory")
		}
		node.Snapshot = snapshot
		if err := p.put(fmt.Sprintf("dedupe/source/%020d/snapshot", n.NodeID), snapshot); err != nil {
			return p.report, err
		}
		if err := p.b.flush(); err != nil {
			return p.report, err
		}
		if progress != nil {
			progress(n.NodeID, "dedupe: comparing identities and sequence impact")
		}
		for _, kind := range []string{"id", "client"} {
			if err := p.groups(n.NodeID, kind, &node); err != nil {
				return p.report, err
			}
		}
		if err := p.b.flush(); err != nil {
			return p.report, err
		}
		if err := p.channels(&node); err != nil {
			return p.report, err
		}
		if err := p.b.flush(); err != nil {
			return p.report, err
		}
		// Both uniqueness rules apply simultaneously. A candidate winner that is
		// also dropped is an unresolved chain, never silently resurrected.
		if err := w.Walk(ctx, []byte(fmt.Sprintf("dedupe/drop/%020d/", n.NodeID)), func(r transfer.SpoolRow) error {
			var drop DedupeDrop
			if err := json.Unmarshal(r.Value, &drop); err != nil {
				return err
			}
			for _, winner := range drop.Winners {
				if _, found, err := w.Get(ctx, []byte(dedupeMessageKey("drop", winner))); err != nil {
					return err
				} else if found {
					drop.Unresolved = true
				}
			}
			if drop.Unresolved {
				p.report.Unresolved++
			}
			return p.out.Encode(drop)
		}); err != nil {
			return p.report, err
		}
		// Include complete counters and bounded samples in the details checksum.
		if err := p.out.Encode(struct {
			Type     string               `json:"type"`
			NodeID   uint64               `json:"node_id,string"`
			Protocol DedupeProtocolImpact `json:"protocol_impact"`
		}{"message_protocol_impact", node.NodeID, node.Protocol}); err != nil {
			return p.report, err
		}
		p.report.Nodes = append(p.report.Nodes, node)
	}
	p.report.ScanComplete = true
	if p.report.Unresolved != 0 {
		p.report.Status = "unresolved"
	}
	p.report.DetailsSHA256 = hex.EncodeToString(h.Sum(nil))
	return p.report, nil
}

func dedupeMessageKey(kind string, m DedupeMessage) string {
	return fmt.Sprintf("dedupe/%s/%020d/%020d/%020d", kind, m.NodeID, m.Owner, m.Sequence)
}

func dedupeGroupKey(kind string, m DedupeMessage) string {
	id := fmt.Sprintf("%020d", m.ID)
	if kind == "client" {
		id = m.ClientKeySHA256
	}
	return fmt.Sprintf("dedupe/%s/%020d/%s", kind, m.NodeID, id)
}

func (p *dedupePlanner) put(key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return p.b.add(transfer.SpoolRow{Key: []byte(key), Value: b})
}

// groups reduces each identity to one maximum and a count, regardless of the
// number of retries in its history. Cross-channel sequences are incomparable.
func (p *dedupePlanner) groups(node uint64, kind string, summary *DedupeNode) error {
	var group DedupeGroup
	flush := func() error {
		if group.Count < 2 {
			return nil
		}
		if kind == "id" {
			summary.MessageIDGroups++
		} else {
			summary.ClientKeyGroups++
		}
		if group.Ambiguous {
			p.report.Unresolved++
		}
		if err := p.out.Encode(group); err != nil {
			return err
		}
		return p.put("dedupe/winner/"+group.Key, group)
	}
	err := p.w.Walk(p.ctx, []byte(fmt.Sprintf("dedupe/%s/%020d/", kind, node)), func(r transfer.SpoolRow) error {
		var m DedupeMessage
		if err := json.Unmarshal(r.Value, &m); err != nil {
			return err
		}
		key := dedupeGroupKey(kind, m)
		if key != group.Key {
			if err := flush(); err != nil {
				return err
			}
			group = DedupeGroup{Type: "duplicate_group", Key: key, Kind: kind, Latest: m}
		}
		group.Count++
		if group.Latest.ChannelSHA256 != m.ChannelSHA256 || group.Latest.Owner != m.Owner {
			group.Ambiguous = true
		}
		if m.Sequence > group.Latest.Sequence {
			group.Latest = m
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

func (p *dedupePlanner) channels(node *DedupeNode) error {
	var ch DedupeChannel
	var identity string
	flush := func() error {
		if ch.Dropped == 0 && ch.SourceGaps == 0 {
			return nil
		}
		node.AffectedChannels++
		node.ChangedSequences += ch.ChangedSequences
		if ch.ChangedSequences != 0 {
			p.report.RenumberingRequired = true
		}
		return p.out.Encode(ch)
	}
	err := p.w.Walk(p.ctx, []byte(fmt.Sprintf("dedupe/message/%020d/", node.NodeID)), func(r transfer.SpoolRow) error {
		var m DedupeMessage
		if err := json.Unmarshal(r.Value, &m); err != nil {
			return err
		}
		if ch.Messages == 0 || ch.Owner != m.Owner {
			if err := flush(); err != nil {
				return err
			}
			ch = DedupeChannel{Type: "channel_impact", NodeID: node.NodeID, Owner: m.Owner}
			identity = m.ChannelSHA256
		}
		if identity != m.ChannelSHA256 {
			return errors.New("colliding source channel hashes")
		}
		if m.Sequence != ch.LastSequence+1 {
			ch.SourceGaps++
			p.report.Unresolved++
		}
		ch.Messages++
		ch.LastSequence = m.Sequence
		if m.StreamParent {
			ch.StreamParents++
		}
		drop, err := p.decision(m)
		if err != nil {
			return err
		}
		if p.excludeCMD && m.CMD {
			drop = DedupeDrop{Type: "candidate_drop", Message: m, Reasons: []string{"cmd"}}
			node.CMDDrops++
			ch.CMDDrops++
		} else if p.excludeStreams && m.Stream {
			drop = DedupeDrop{Type: "candidate_drop", Message: m, Reasons: []string{"stream"}}
			node.StreamDrops++
			ch.StreamDrops++
		}
		if len(drop.Winners) != 0 || (p.excludeCMD && m.CMD) || (p.excludeStreams && m.Stream) {
			node.Dropped++
			ch.Dropped++
			if ch.FirstDrop == 0 {
				ch.FirstDrop = m.Sequence
			}
			if m.StreamParent {
				ch.DroppedStreamParents++
				node.DroppedStreamParents++
			}
			if len(m.UnsupportedFields) > 0 {
				node.Protocol.OmittedUnsupported++
			}
			for _, field := range m.UnsupportedFields {
				node.Protocol.OmittedFields[field]++
			}
			return p.put(dedupeMessageKey("drop", m), drop)
		}
		node.Protocol.Retained++
		if len(m.UnsupportedFields) > 0 {
			node.Protocol.RetainedUnsupported++
		}
		for _, field := range m.UnsupportedFields {
			node.Protocol.RetainedFields[field]++
			if len(node.Protocol.Samples[field]) < 3 {
				node.Protocol.Samples[field] = append(node.Protocol.Samples[field], m)
			}
		}
		ch.Retained++
		if m.Sequence != ch.Retained {
			ch.ChangedSequences++
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// decision evaluates both identity maxima without resolving overlapping chains.
func (p *dedupePlanner) decision(m DedupeMessage) (DedupeDrop, error) {
	drop := DedupeDrop{Type: "candidate_drop", Message: m}
	for _, kind := range []string{"id", "client"} {
		if kind == "client" && m.ClientKeySHA256 == "" {
			continue
		}
		data, found, err := p.w.Get(p.ctx, []byte("dedupe/winner/"+dedupeGroupKey(kind, m)))
		if err != nil {
			return drop, err
		}
		if !found {
			continue
		}
		var group DedupeGroup
		if err := json.Unmarshal(data, &group); err != nil {
			return drop, err
		}
		if group.Ambiguous {
			drop.Unresolved = true
			continue
		}
		if group.Latest.Sequence <= m.Sequence {
			continue
		}
		drop.Reasons = append(drop.Reasons, kind)
		if len(drop.Winners) == 0 || dedupeMessageKey("", drop.Winners[0]) != dedupeMessageKey("", group.Latest) {
			drop.Winners = append(drop.Winners, group.Latest)
		}
	}
	return drop, nil
}
