package migration

import (
	"bytes"
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

func markedChannelConfig(row Row) bool {
	if row.Table != "ChannelClusterConfig" || row.Kind != Primary {
		return false
	}
	for _, name := range []string{"MigrateFrom", "MigrateTo"} {
		for _, b := range row.Fields[name] {
			if b != 0 {
				return true
			}
		}
	}
	return false
}

func capturedCommandKey(node uint64, c RawConfigCommand) []byte {
	return []byte(fmt.Sprintf("source/%020d/config-commands/%010d/%020d", node, c.Slot, c.Index))
}

// walkCapturedCommands hashes the ordered original bytes and their placement.
// This count/digest is part of Capture.Digest and the portable archive manifest.
func walkCapturedCommands(ctx context.Context, w Workspace, node uint64, visit func(RawConfigCommand) error) (uint64, string, error) {
	h := sha256.New()
	enc := json.NewEncoder(h)
	var count uint64
	err := w.Walk(ctx, []byte(fmt.Sprintf("source/%020d/config-commands/", node)), func(row transfer.SpoolRow) error {
		var c RawConfigCommand
		if err := json.Unmarshal(row.Value, &c); err != nil {
			return err
		}
		if c.Index == 0 || !bytes.Equal(row.Key, capturedCommandKey(node, c)) {
			return errors.New("captured config command identity mismatch")
		}
		if err := enc.Encode(row); err != nil {
			return err
		}
		count++
		if visit != nil {
			return visit(c)
		}
		return nil
	})
	return count, hex.EncodeToString(h.Sum(nil)), err
}

// capturedAuthoritySource rebuilds diagnostic inputs from immutable capture,
// including raw commands. No prior diagnostic verdict is imported or trusted.
type capturedAuthoritySource struct {
	w       Workspace
	capture SourceCapture
	decoder AuthorityCommandDecoder
}

func (s capturedAuthoritySource) ReadAuthorityNode(ctx context.Context, n NodeOptions, rows func(Row) error, files func(SourceFile) error, logs func(ChannelConfigLog) error) (NodeSnapshot, error) {
	var snapshot NodeSnapshot
	for _, node := range s.capture.Nodes {
		if node.NodeID == n.NodeID {
			snapshot = node
			break
		}
	}
	var evidence *CapturedAuthority
	for i := range s.capture.Authority {
		if s.capture.Authority[i].NodeID == n.NodeID {
			evidence = &s.capture.Authority[i]
			break
		}
	}
	if snapshot.NodeID == 0 || evidence == nil || evidence.ShardCount != n.ShardCount {
		return snapshot, errors.New("captured authority node evidence missing")
	}
	count, digest, err := walkCapturedCommands(ctx, s.w, n.NodeID, nil)
	if err != nil {
		return snapshot, err
	}
	if count != evidence.Commands || digest != evidence.SHA256 {
		return snapshot, errors.New("captured authority command digest mismatch")
	}
	if files != nil {
		prefix := fmt.Sprintf("source/%020d/files/", n.NodeID)
		if err := s.w.Walk(ctx, []byte(prefix), func(row transfer.SpoolRow) error {
			var file SourceFile
			if err := json.Unmarshal(row.Value, &file); err != nil {
				return err
			}
			if string(row.Key) != prefix+file.Path {
				return errors.New("captured source file identity mismatch")
			}
			return files(file)
		}); err != nil {
			return snapshot, err
		}
	}
	if err := walkSourceRows(ctx, s.w, n.NodeID, rows); err != nil {
		return snapshot, err
	}
	_, _, err = walkCapturedCommands(ctx, s.w, n.NodeID, func(c RawConfigCommand) error { return logs(s.decoder.DecodeAuthorityCommand(c)) })
	return snapshot, err
}

// certifyCapturedTransitions runs before any selection. Its private workspace
// cannot be substituted with an external authority report. Legacy captures
// lacking raw command evidence keep the original strict decoder behavior.
func certifyCapturedTransitions(ctx context.Context, capture SourceCapture, w Workspace, decoder RecordDecoder) (RecordDecoder, string, error) {
	// Validate captured command integrity even when no transition consumes it.
	// Portable archival must not silently lose supposedly preserved commands.
	if len(capture.Authority) > 0 {
		if len(capture.Authority) != len(capture.Nodes) {
			return nil, "", errors.New("incomplete captured authority inventory")
		}
		seen := map[uint64]bool{}
		for _, a := range capture.Authority {
			known := false
			for _, n := range capture.Nodes {
				known = known || n.NodeID == a.NodeID
			}
			if !known || seen[a.NodeID] || a.ShardCount < 1 || a.ShardCount > 1024 {
				return nil, "", errors.New("invalid captured authority inventory")
			}
			seen[a.NodeID] = true
			count, digest, err := walkCapturedCommands(ctx, w, a.NodeID, nil)
			if err != nil {
				return nil, "", err
			}
			if count != a.Commands || digest != a.SHA256 {
				return nil, "", errors.New("captured authority command digest mismatch")
			}
		}
	}
	if capture.MarkedConfigurations == 0 {
		return decoder, "", nil
	}
	original, ok := decoder.(AuthorityCommandDecoder)
	if !ok || len(capture.Authority) != len(capture.Nodes) {
		return nil, "", errors.New("marked source configurations require captured original command evidence")
	}
	plan := Plan{}
	for _, node := range capture.Nodes {
		var shardCount int
		matches := 0
		for _, a := range capture.Authority {
			if a.NodeID == node.NodeID {
				shardCount = a.ShardCount
				matches++
			}
		}
		if matches != 1 || shardCount < 1 || shardCount > 1024 {
			return nil, "", errors.New("invalid captured authority inventory")
		}
		plan.Sources = append(plan.Sources, NodeOptions{NodeID: node.NodeID, Options: Options{ShardCount: shardCount}})
	}
	proofs := scopedWorkspace{Workspace: w, prefix: "selection-authority-v3/"}
	batch := &captureBatch{ctx: ctx, workspace: proofs}
	report, err := auditSourceAuthority(ctx, plan, proofs, capturedAuthoritySource{w: w, capture: capture, decoder: original}, original, io.Discard, nil, func(c AuthorityChannel) error {
		if c.CandidateNode == 0 {
			return fmt.Errorf("source transition %d remains %s: %v", c.Owner, c.Class, c.Reasons)
		}
		data, err := json.Marshal(c)
		if err != nil {
			return err
		}
		return batch.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("proof/%020d", c.Owner)), Value: data})
	})
	if err != nil {
		return nil, "", err
	}
	if report.CommandExitCode() != 0 || report.Channels == 0 {
		return nil, "", errors.New("captured source transition authority remains unresolved")
	}
	if err := batch.flush(); err != nil {
		return nil, "", err
	}
	digest := diagnosticSHA([]byte(capture.Digest + ":" + report.DetailsSHA256))
	return provenTransitionDecoder{RecordDecoder: decoder, original: original, ctx: ctx, proofs: proofs}, digest, nil
}

type provenTransitionDecoder struct {
	RecordDecoder
	original AuthorityCommandDecoder
	ctx      context.Context
	proofs   Workspace
}

func (d provenTransitionDecoder) Describe(row Row, id RecordIdentity) (RecordDescription, error) {
	if !markedChannelConfig(row) {
		return d.RecordDecoder.Describe(row, id)
	}
	c, err := d.original.InspectChannelConfig(row)
	if err != nil {
		return RecordDescription{}, err
	}
	data, found, err := d.proofs.Get(d.ctx, []byte(fmt.Sprintf("proof/%020d", c.Owner)))
	if err != nil {
		return RecordDescription{}, err
	}
	if !found {
		return RecordDescription{}, errors.New("source config lacks a rebuilt transition proof")
	}
	var proof AuthorityChannel
	if err := json.Unmarshal(data, &proof); err != nil {
		return RecordDescription{}, err
	}
	matching := false
	for _, copy := range proof.ConfigCopies {
		if copy.VersionRule != "" && copy.Config.SHA256 == c.SHA256 {
			matching = true
		}
	}
	if !matching || proof.CandidateNode != c.Leader || c.IdentitySHA256 != diagnosticSHA([]byte(IdentityKey(id.Channel.ID, id.Channel.Type))) {
		return RecordDescription{}, errors.New("source config differs from its rebuilt transition proof")
	}
	authority := &ChannelAuthority{Channel: id.Channel, Leader: c.Leader, Replicas: append([]uint64(nil), c.Replicas...), Term: c.Term, Version: c.Version}
	sort.Slice(authority.Replicas, func(i, j int) bool { return authority.Replicas[i] < authority.Replicas[j] })
	// All owner-Slot copies already matched the complete raw field digest.
	// Keep markers/version intact in the comparable value and archived row.
	comparable, err := json.Marshal(row.Fields)
	return RecordDescription{Key: IdentityKey(id.Channel.ID, id.Channel.Type), Comparable: comparable, Authority: authority}, err
}
