package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
)

// PluginNodeMapping explicitly chooses one source node's settings for a target.
// A source may supply several targets; all original nodes remain in the capture.
type PluginNodeMapping struct {
	SourceNode uint64 `json:"source_node"`
	TargetNode uint64 `json:"target_node"`
}

// PluginConfigMapping overrides only effective configuration, never the
// receiving node's registration, enabled flag or timestamps.
type PluginConfigMapping struct {
	PluginNo   string `json:"plugin_no"`
	SourceNode uint64 `json:"source_node"`
}

// PluginConfigSource records the exact captured row supplying an override.
type PluginConfigSource struct {
	SourceNode      uint64
	SourceKey       []byte
	SourceRowSHA256 string
}

// PluginSettingsReport certifies only config assignment from captured rows.
// Runtime compatibility, executable installation and user binding are separate.
type PluginSettingsReport struct {
	CaptureDigest string                `json:"capture_digest"`
	PlanDigest    string                `json:"plan_digest"`
	Records       uint64                `json:"records"`
	ByTarget      map[uint64]uint64     `json:"records_by_target"`
	Digest        string                `json:"digest"`
	ConfigSources []PluginConfigMapping `json:"config_sources,omitempty"`
}

// MappedPluginSettings retains original fields beside native desired state.
// This private workspace record contains config secrets and must not be logged.
type MappedPluginSettings struct {
	SourceNode, TargetNode uint64
	SourceKey              []byte
	SourceRowSHA256        string
	Original               SourcePlugin
	Desired                pluginhost.DesiredState
	ConfigSource           *PluginConfigSource `json:"ConfigSource,omitempty"`
}

func validatePluginNodes(plan Plan) error {
	if len(plan.PluginNodes) == 0 {
		if len(plan.PluginConfigs) != 0 {
			return errors.New("plugin config overrides require explicit node assignments")
		}
		return nil
	}
	if len(plan.Sources) == 0 || len(plan.Target.Nodes) == 0 || len(plan.PluginNodes) != len(plan.Target.Nodes) {
		return errors.New("plugin node assignment requires exactly one source for every target")
	}
	sources, targets := map[uint64]bool{}, map[uint64]bool{}
	for _, n := range plan.Sources {
		if n.NodeID == 0 || sources[n.NodeID] {
			return errors.New("invalid plugin source node set")
		}
		sources[n.NodeID] = true
	}
	for _, n := range plan.Target.Nodes {
		if n.NodeID == 0 || targets[n.NodeID] {
			return errors.New("invalid plugin target node set")
		}
		targets[n.NodeID] = true
	}
	plugins := map[string]bool{}
	for _, p := range plan.PluginConfigs {
		if !mappedPluginNo.MatchString(p.PluginNo) || p.PluginNo == "." || p.PluginNo == ".." || !sources[p.SourceNode] || plugins[p.PluginNo] {
			return errors.New("plugin config override has an invalid, repeated or unknown identity")
		}
		plugins[p.PluginNo] = true
	}
	for _, n := range plan.PluginNodes {
		if !sources[n.SourceNode] || !targets[n.TargetNode] {
			return errors.New("plugin node assignment contains an unknown source or an unknown/repeated target")
		}
		delete(targets, n.TargetNode)
	}
	return nil
}

var mappedPluginNo = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// PreparePluginSettings derives exact assignments without selecting business
// replicas or authorizing startup. A fresh archive rebuild recomputes the same
// records from raw rows; neither a report nor existing scratch rows are trusted.
func PreparePluginSettings(ctx context.Context, plan Plan, capture SourceCapture, w Workspace, decoder BusinessDecoder) (*PluginSettingsReport, error) {
	if err := validatePluginNodes(plan); err != nil {
		return nil, err
	}
	if len(plan.PluginNodes) == 0 {
		return nil, nil
	}
	if ctx == nil || w == nil || decoder == nil || capture.Digest == "" {
		return nil, errors.New("plugin settings require complete captured sources")
	}
	seen := map[uint64]bool{}
	for _, n := range capture.Nodes {
		if seen[n.NodeID] {
			return nil, errors.New("duplicate plugin capture node")
		}
		seen[n.NodeID] = true
	}
	if len(seen) != len(plan.Sources) {
		return nil, errors.New("plugin capture node set differs from plan")
	}
	for _, source := range plan.Sources {
		if !seen[source.NodeID] {
			return nil, errors.New("plugin capture source missing")
		}
	}
	report := &PluginSettingsReport{CaptureDigest: capture.Digest, PlanDigest: plan.Digest(), ByTarget: map[uint64]uint64{}, ConfigSources: append([]PluginConfigMapping(nil), plan.PluginConfigs...)}
	sort.Slice(report.ConfigSources, func(i, j int) bool { return report.ConfigSources[i].PluginNo < report.ConfigSources[j].PluginNo })
	overrides := map[string]uint64{}
	for _, p := range plan.PluginConfigs {
		overrides[p.PluginNo] = p.SourceNode
	}
	originalPrefix := "plugin-settings-original/v2/" + capture.Digest + "/" + plan.Digest() + "/"
	ordered := append([]PluginNodeMapping(nil), plan.PluginNodes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].TargetNode < ordered[j].TargetNode })
	batch := &captureBatch{ctx: ctx, workspace: w}
	for _, source := range plan.Sources {
		err := walkSourceRows(ctx, w, source.NodeID, func(row Row) error {
			if row.Table != "Plugin" || row.Kind != Primary {
				return nil
			}
			facts, err := decoder.DecodeBusiness(row, RecordIdentity{})
			if err != nil {
				return err
			}
			if facts.Plugin == nil {
				return errors.New("original plugin settings were not decoded")
			}
			p := *facts.Plugin
			if !mappedPluginNo.MatchString(p.No) || p.No == "." || p.No == ".." || p.Status > 2 {
				return errors.New("plugin settings cannot be represented by the native store")
			}
			state := pluginhost.DesiredState{No: p.No, Config: append(json.RawMessage(nil), p.Config...), Enabled: p.Status != 2}
			if p.CreatedAt != nil {
				state.CreatedAt = *p.CreatedAt
			}
			if p.UpdatedAt != nil {
				state.UpdatedAt = *p.UpdatedAt
			}
			original, err := json.Marshal(row)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(original)
			record := MappedPluginSettings{SourceNode: source.NodeID, SourceKey: sourceRowKey(source.NodeID, row), SourceRowSHA256: hex.EncodeToString(sum[:]), Original: p, Desired: state}
			data, err := MarshalState(record)
			if err != nil {
				return err
			}
			// The verified original primary identity makes this output key unique.
			key := originalPrefix + fmt.Sprintf("%020d/%x", source.NodeID, []byte(p.No))
			if err := batch.add(transfer.SpoolRow{Key: []byte(key), Value: data}); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("source node %d plugin settings: %w", source.NodeID, err)
		}
	}
	if err := batch.flush(); err != nil {
		return nil, err
	}
	assigned := map[string]int{}
	for _, mapping := range ordered {
		report.ByTarget[mapping.TargetNode] = 0
		err := w.Walk(ctx, []byte(originalPrefix+fmt.Sprintf("%020d/", mapping.SourceNode)), func(row transfer.SpoolRow) error {
			var record MappedPluginSettings
			if err := UnmarshalState(row.Value, &record); err != nil {
				return err
			}
			record.TargetNode = mapping.TargetNode
			if source := overrides[record.Desired.No]; source != 0 {
				data, found, err := w.Get(ctx, []byte(originalPrefix+fmt.Sprintf("%020d/%x", source, []byte(record.Desired.No))))
				if err != nil {
					return err
				}
				if !found {
					return errors.New("selected plugin config is absent from its source node")
				}
				var original MappedPluginSettings
				if err := UnmarshalState(data, &original); err != nil {
					return err
				}
				record.Desired.Config = append(json.RawMessage(nil), original.Original.Config...)
				record.ConfigSource = &PluginConfigSource{SourceNode: source, SourceKey: original.SourceKey, SourceRowSHA256: original.SourceRowSHA256}
				assigned[record.Desired.No]++
			}
			data, err := MarshalState(record)
			if err != nil {
				return err
			}
			key := pluginSettingsPrefix(*report) + fmt.Sprintf("%020d/%x", record.TargetNode, []byte(record.Desired.No))
			if err := batch.add(transfer.SpoolRow{Key: []byte(key), Value: data}); err != nil {
				return err
			}
			report.Records++
			report.ByTarget[record.TargetNode]++
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	for no := range overrides {
		if assigned[no] != len(plan.PluginNodes) {
			return nil, errors.New("plugin config override requires the plugin on every mapped source node")
		}
	}
	if err := batch.flush(); err != nil {
		return nil, err
	}
	digest, err := pluginSettingsDigest(ctx, w, *report)
	if err != nil {
		return nil, err
	}
	report.Digest = digest
	return report, nil
}

func pluginSettingsPrefix(report PluginSettingsReport) string {
	return "plugin-settings/v1/" + report.CaptureDigest + "/" + report.PlanDigest + "/"
}

// WalkPluginSettings exposes sensitive assigned state only to offline adapters.
// Callers must first recompute PreparePluginSettings from their bound capture.
func WalkPluginSettings(ctx context.Context, w Workspace, report PluginSettingsReport, visit func(MappedPluginSettings) error) error {
	if report.Digest == "" || report.CaptureDigest == "" || report.PlanDigest == "" {
		return errors.New("plugin settings require a completed assignment")
	}
	digest, err := pluginSettingsDigest(ctx, w, report)
	if err != nil {
		return err
	}
	if digest != report.Digest {
		return errors.New("plugin settings digest mismatch")
	}
	return w.Walk(ctx, []byte(pluginSettingsPrefix(report)), func(row transfer.SpoolRow) error {
		var record MappedPluginSettings
		if err := UnmarshalState(row.Value, &record); err != nil {
			return err
		}
		return visit(record)
	})
}

func pluginSettingsDigest(ctx context.Context, w Workspace, report PluginSettingsReport) (string, error) {
	h := sha256.New()
	enc := json.NewEncoder(h)
	if err := enc.Encode(struct {
		Capture, Plan string
		ConfigSources []PluginConfigMapping `json:",omitempty"`
	}{report.CaptureDigest, report.PlanDigest, report.ConfigSources}); err != nil {
		return "", err
	}
	var count uint64
	byTarget := map[uint64]uint64{}
	err := w.Walk(ctx, []byte(pluginSettingsPrefix(report)), func(row transfer.SpoolRow) error {
		var record MappedPluginSettings
		if err := UnmarshalState(row.Value, &record); err != nil {
			return err
		}
		if string(row.Key) != pluginSettingsPrefix(report)+fmt.Sprintf("%020d/%x", record.TargetNode, []byte(record.Desired.No)) {
			return errors.New("plugin settings key mismatch")
		}
		count++
		byTarget[record.TargetNode]++
		return enc.Encode(row)
	})
	if err != nil {
		return "", err
	}
	if count != report.Records {
		return "", errors.New("plugin settings count mismatch")
	}
	for node, count := range byTarget {
		if report.ByTarget[node] != count {
			return "", errors.New("plugin settings target count mismatch")
		}
	}
	for node, count := range report.ByTarget {
		if byTarget[node] != count {
			return "", errors.New("plugin settings target count mismatch")
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
