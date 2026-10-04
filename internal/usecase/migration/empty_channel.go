package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// EmptyChannelDecoder exposes fixed original-format evidence, not a general
// exclusion filter. Any business reference prevents archival-only treatment.
type EmptyChannelDecoder interface {
	InspectEmptyChannel(Row, int) (*EmptyChannelRow, error)
	EmptyChannelReference(Row) (bool, error)
	DecodeEmptyChannelCommand(RawConfigCommand) (ChannelConfigLog, bool, error)
}

type EmptyChannelRow struct{ Config *ChannelConfigEvidence }

// EmptyChannelProof binds the full immutable capture, the two administrative
// primaries per source copy, and every retained original empty config command.
type EmptyChannelProof struct {
	CaptureDigest string `json:"capture_digest"`
	Rows          uint64 `json:"rows"`
	Commands      uint64 `json:"commands"`
	SHA256        string `json:"sha256"`
}

type emptyChannelCopy struct {
	info     string
	config   *ChannelConfigEvidence
	logs     string
	commands uint64
	last     *ChannelConfigLog
}

// certifyEmptyChannels runs before catalog construction. It never repairs raw
// data, and archives no row until absence, replica and command proofs all pass.
func certifyEmptyChannels(ctx context.Context, capture SourceCapture, w Workspace, original OriginalDecoder, p *MetadataPolicy) (OriginalDecoder, error) {
	if p == nil || !p.ArchiveEmptyChannels {
		return original, nil
	}
	if err := validateCapturedAuthority(capture.Nodes); err != nil {
		return nil, err
	}
	d, ok := original.(EmptyChannelDecoder)
	if !ok {
		return nil, errors.New("empty-channel proof requires original decoder")
	}
	a, ok := original.(AuthorityCommandDecoder)
	if !ok {
		return nil, errors.New("empty-channel proof requires original command decoder")
	}
	if len(capture.Authority) != len(capture.Nodes) {
		return nil, errors.New("empty-channel proof requires complete command inventory")
	}
	proof := &EmptyChannelProof{CaptureDigest: capture.Digest}
	base := "empty-channel/v1/" + capture.Digest + "/"
	wrapped := &emptyChannelDecoder{OriginalDecoder: original, authority: a, empty: d, ctx: ctx, w: w, base: base, rows: map[string]*EmptyChannelRow{}, proof: proof}
	copies := map[uint64]*emptyChannelCopy{}
	h := sha256.New()
	enc := json.NewEncoder(h)
	shards := map[uint64]int{}
	for _, entry := range capture.Authority {
		if entry.ShardCount < 1 || shards[entry.NodeID] != 0 {
			return nil, errors.New("invalid empty-channel shard inventory")
		}
		shards[entry.NodeID] = entry.ShardCount
	}
	for _, node := range capture.Nodes {
		copy := &emptyChannelCopy{}
		copies[node.NodeID] = copy
		if shards[node.NodeID] < 1 {
			return nil, errors.New("missing empty-channel shard inventory")
		}
		if err := walkSourceRows(ctx, w, node.NodeID, func(r Row) error {
			facts, err := d.InspectEmptyChannel(r, shards[node.NodeID])
			if err != nil {
				return err
			}
			if facts == nil {
				ref, err := d.EmptyChannelReference(r)
				if err != nil {
					return err
				}
				if ref {
					return errors.New("empty channel has a business reference")
				}
				return nil
			}
			raw, err := json.Marshal(r)
			if err != nil {
				return err
			}
			wrapped.rows[diagnosticSHA(raw)] = facts
			fields, err := json.Marshal(r.Fields)
			if err != nil {
				return err
			}
			if facts.Config == nil {
				if copy.info != "" {
					return errors.New("duplicate empty-channel body")
				}
				copy.info = diagnosticSHA(fields)
			} else {
				if copy.config != nil {
					return errors.New("duplicate empty-channel config")
				}
				copy.config = facts.Config
			}
			proof.Rows++
			return enc.Encode(struct {
				Node uint64
				Row  Row
			}{node.NodeID, r})
		}); err != nil {
			return nil, fmt.Errorf("empty-channel source node %d: %w", node.NodeID, err)
		}
	}
	// A plan may enable this rule before knowing whether empty records exist.
	// Without candidates no decoder exception is installed.
	if proof.Rows == 0 {
		return original, nil
	}
	var owner SourceSlot
	for _, s := range capture.Nodes[0].Config.Slots {
		if s.ID == 0 {
			owner = s
		}
	}
	reference := copies[owner.Leader]
	if reference == nil || reference.info == "" || reference.config == nil {
		return nil, errors.New("empty-channel authority pair is missing")
	}
	for _, node := range owner.Replicas {
		c := copies[node]
		if c == nil || c.info == "" || c.config == nil {
			return nil, errors.New("empty-channel formal replica pair is missing")
		}
	}
	batch := &captureBatch{ctx: ctx, workspace: w}
	for _, node := range capture.Nodes {
		copy := copies[node.NodeID]
		if (copy.info == "") != (copy.config == nil) {
			return nil, errors.New("incomplete empty-channel archival pair")
		}
		if copy.config != nil && (copy.info != reference.info || copy.config.SHA256 != reference.config.SHA256) {
			return nil, errors.New("empty-channel replica records conflict")
		}
		logHash := sha256.New()
		logEnc := json.NewEncoder(logHash)
		count, digest, err := walkCapturedCommands(ctx, w, node.NodeID, func(raw RawConfigCommand) error {
			log, empty, err := d.DecodeEmptyChannelCommand(raw)
			if err != nil {
				return err
			}
			if !empty {
				return nil
			}
			progress, err := capturedSlotProgress(node, raw.Slot)
			if err != nil {
				return err
			}
			if raw.Slot != 0 || raw.Index > progress.AppliedIndex || log.Config.Status != 0 || log.Config.MigrateFrom != 0 || log.Config.MigrateTo != 0 || len(log.Config.Learners) != 0 {
				return errors.New("empty-channel command has unproven placement or transition")
			}
			if copy.config == nil {
				return errors.New("empty-channel command lacks current administrative pair")
			}
			copy.commands++
			copy.last = &log
			proof.Commands++
			if err := logEnc.Encode(raw); err != nil {
				return err
			}
			if err := enc.Encode(struct {
				Node    uint64
				Command RawConfigCommand
			}{node.NodeID, raw}); err != nil {
				return err
			}
			return batch.add(transfer.SpoolRow{Key: []byte(base + "command/" + emptyCommandIdentity(raw)), Value: []byte{1}})
		})
		if err != nil {
			return nil, fmt.Errorf("empty-channel commands on node %d: %w", node.NodeID, err)
		}
		var bound *CapturedAuthority
		for i := range capture.Authority {
			if capture.Authority[i].NodeID == node.NodeID {
				bound = &capture.Authority[i]
			}
		}
		if bound == nil || bound.Commands != count || bound.SHA256 != digest {
			return nil, errors.New("empty-channel captured command inventory mismatch")
		}
		copy.logs = hex.EncodeToString(logHash.Sum(nil))
		if copy.config != nil && (copy.last == nil || originalConfigVersionRule(AuthorityConfigCopy{Config: *copy.config, LastApplied: copy.last}) == "") {
			return nil, errors.New("empty-channel retained command does not prove current config")
		}
	}
	for node, copy := range copies {
		if slices.Contains(owner.Replicas, node) && (copy.commands != reference.commands || copy.logs != reference.logs) {
			return nil, errors.New("empty-channel formal replica command histories conflict")
		}
	}
	if err := batch.flush(); err != nil {
		return nil, err
	}
	proof.SHA256 = hex.EncodeToString(h.Sum(nil))
	return wrapped, nil
}

func emptyCommandIdentity(raw RawConfigCommand) string {
	data, _ := json.Marshal(raw)
	return diagnosticSHA(data)
}

type emptyChannelDecoder struct {
	OriginalDecoder
	authority AuthorityCommandDecoder
	empty     EmptyChannelDecoder
	ctx       context.Context
	w         Workspace
	base      string
	rows      map[string]*EmptyChannelRow // at most two certified records per node
	proof     *EmptyChannelProof
}

func (d *emptyChannelDecoder) EmptyChannelProof() *EmptyChannelProof { return d.proof }
func (d *emptyChannelDecoder) certified(r Row) (*EmptyChannelRow, bool) {
	if r.Kind != Primary || (r.Table != "ChannelInfo" && r.Table != "ChannelClusterConfig") {
		return nil, false
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, false
	}
	v, ok := d.rows[diagnosticSHA(b)]
	return v, ok
}
func (d *emptyChannelDecoder) Identify(r Row) (RecordIdentity, error) {
	if _, ok := d.certified(r); ok {
		return RecordIdentity{}, nil
	}
	return d.OriginalDecoder.Identify(r)
}
func (d *emptyChannelDecoder) Describe(r Row, id RecordIdentity) (RecordDescription, error) {
	if _, ok := d.certified(r); ok {
		return RecordDescription{ArchivedEmptyChannel: true}, nil
	}
	return d.OriginalDecoder.Describe(r, id)
}
func (d *emptyChannelDecoder) DescribeIndexes(r Row, id RecordIdentity, shards int) (SourceIndexFacts, error) {
	if _, ok := d.certified(r); ok {
		return SourceIndexFacts{}, nil
	}
	return d.OriginalDecoder.DescribeIndexes(r, id, shards)
}
func (d *emptyChannelDecoder) InspectChannelConfig(r Row) (ChannelConfigEvidence, error) {
	if f, ok := d.certified(r); ok && f.Config != nil {
		return *f.Config, nil
	}
	return d.authority.InspectChannelConfig(r)
}
func (d *emptyChannelDecoder) InspectMessage(r Row, shards int) (MessageEvidence, error) {
	return d.authority.InspectMessage(r, shards)
}
func (d *emptyChannelDecoder) DecodeAuthorityCommand(raw RawConfigCommand) ChannelConfigLog {
	log, empty, err := d.empty.DecodeEmptyChannelCommand(raw)
	if err != nil || !empty {
		return d.authority.DecodeAuthorityCommand(raw)
	}
	_, found, err := d.w.Get(d.ctx, []byte(d.base+"command/"+emptyCommandIdentity(raw)))
	if !found || err != nil {
		return d.authority.DecodeAuthorityCommand(raw)
	}
	return log
}
