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

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
)

// PluginSettingsVerification records source-derived config checks, not runtime
// compatibility or permission to start the target cluster.
type PluginSettingsVerification struct {
	CaptureDigest string            `json:"capture_digest"`
	PlanDigest    string            `json:"plan_digest"`
	Records       uint64            `json:"records"`
	ByTarget      map[uint64]uint64 `json:"records_by_target"`
	Digest        string            `json:"digest"`
}

// EqualPluginState compares native JSON semantically without rounding large
// integers through float64. Registration flags and timestamps remain exact.
func EqualPluginState(a, b pluginhost.DesiredState) (bool, error) {
	if a.No != b.No || a.Enabled != b.Enabled || !a.CreatedAt.Equal(b.CreatedAt) || !a.UpdatedAt.Equal(b.UpdatedAt) {
		return false, nil
	}
	canonical := func(raw json.RawMessage) ([]byte, error) {
		if len(raw) == 0 {
			return nil, nil
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			return nil, errors.New("invalid plugin configuration JSON")
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			return nil, errors.New("trailing plugin configuration JSON")
		}
		return json.Marshal(v)
	}
	x, err := canonical(a.Config)
	if err != nil {
		return false, err
	}
	y, err := canonical(b.Config)
	if err != nil {
		return false, err
	}
	return bytes.Equal(x, y), nil
}

// VerifyPluginSettings recomputes expectations from original captured Plugin
// rows and the explicit plan. It never reads assigned settings or their report.
func VerifyPluginSettings(ctx context.Context, plan Plan, capture SourceCapture, w Workspace, decoder BusinessDecoder, inspector TargetInspector) (out PluginSettingsVerification, err error) {
	if ctx == nil || capture.Digest == "" || w == nil || decoder == nil || inspector == nil {
		return out, errors.New("plugin verification requires captured sources and target inspector")
	}
	if err := validatePluginNodes(plan); err != nil {
		return out, err
	}
	out = PluginSettingsVerification{CaptureDigest: capture.Digest, PlanDigest: plan.Digest(), ByTarget: map[uint64]uint64{}}
	prefix := "verification/plugin-original/v1/" + capture.Digest + "/" + plan.Digest() + "/"
	key := func(node uint64, no string) []byte { return []byte(prefix + fmt.Sprintf("%020d/%x", node, []byte(no))) }
	b := &captureBatch{ctx: ctx, workspace: w}
	sources := map[uint64]bool{}
	for _, n := range capture.Nodes {
		if sources[n.NodeID] {
			return out, errors.New("duplicate plugin verification source")
		}
		sources[n.NodeID] = true
	}
	if len(plan.PluginNodes) > 0 && len(sources) != len(plan.Sources) {
		return out, errors.New("plugin verification capture node set differs from plan")
	}
	overrides := map[string]uint64{}
	for _, p := range plan.PluginConfigs {
		overrides[p.PluginNo] = p.SourceNode
	}
	for _, n := range plan.Sources {
		if !sources[n.NodeID] {
			return out, errors.New("plugin verification source missing")
		}
		err := walkSourceRows(ctx, w, n.NodeID, func(row Row) error {
			if row.Table != "Plugin" || row.Kind != Primary {
				return nil
			}
			f, err := decoder.DecodeBusiness(row, RecordIdentity{})
			if err != nil {
				return err
			}
			if f.Plugin == nil {
				return errors.New("plugin verification source was not decoded")
			}
			p := f.Plugin
			if !mappedPluginNo.MatchString(p.No) || p.No == "." || p.No == ".." || p.Status > 2 {
				return errors.New("plugin verification source is not native-compatible")
			}
			data, err := MarshalState(p)
			if err != nil {
				return err
			}
			return b.add(transfer.SpoolRow{Key: key(n.NodeID, p.No), Value: data})
		})
		if err != nil {
			return out, err
		}
	}
	if err := b.flush(); err != nil {
		return out, err
	}
	proofPrefix := "verification/plugin-proof/v1/" + capture.Digest + "/" + plan.Digest() + "/"
	hash := sha256.New()
	evidence := json.NewEncoder(hash)
	if err := evidence.Encode(struct{ Capture, Plan string }{capture.Digest, plan.Digest()}); err != nil {
		return out, err
	}
	for _, node := range plan.Target.Nodes {
		var source uint64
		for _, m := range plan.PluginNodes {
			if m.TargetNode == node.NodeID {
				source = m.SourceNode
			}
		}
		// Disk keys are unique by source and plugin; counts detect missing files.
		var expected uint64
		if source != 0 {
			seenOverrides := map[string]bool{}
			err := w.Walk(ctx, []byte(prefix+fmt.Sprintf("%020d/", source)), func(row transfer.SpoolRow) error {
				var p SourcePlugin
				if err := UnmarshalState(row.Value, &p); err != nil {
					return err
				}
				expected++
				if overrides[p.No] != 0 {
					seenOverrides[p.No] = true
				}
				return nil
			})
			if err != nil {
				return out, err
			}
			if len(seenOverrides) != len(overrides) {
				return out, errors.New("plugin config override is absent from a mapped source")
			}
		}
		view, err := inspector.Open(ctx, plan.Target, node)
		if err != nil {
			return out, err
		}
		var actual uint64
		verifyErr := view.WalkPluginStates(ctx, func(got pluginhost.DesiredState) error {
			data, found, err := w.Get(ctx, key(source, got.No))
			if err != nil {
				return err
			}
			if source == 0 || !found {
				return errors.New("unexpected native plugin settings")
			}
			var p SourcePlugin
			if err := UnmarshalState(data, &p); err != nil {
				return err
			}
			// Derive these fields independently of the assignment converter.
			want := pluginhost.DesiredState{No: p.No, Enabled: p.Status == 0 || p.Status == 1, Config: p.Config}
			if p.CreatedAt != nil {
				want.CreatedAt = *p.CreatedAt
			}
			if p.UpdatedAt != nil {
				want.UpdatedAt = *p.UpdatedAt
			}
			if selected := overrides[p.No]; selected != 0 {
				raw, found, err := w.Get(ctx, key(selected, p.No))
				if err != nil {
					return err
				}
				if !found {
					return errors.New("selected plugin verification config is missing")
				}
				var original SourcePlugin
				if err := UnmarshalState(raw, &original); err != nil {
					return err
				}
				want.Config = original.Config
			}
			equal, err := EqualPluginState(want, got)
			if err != nil {
				return err
			}
			if !equal {
				return errors.New("native plugin settings differ from original rows and approved config policy")
			}
			actual++
			data, err = MarshalState(want)
			if err != nil {
				return err
			}
			return b.add(transfer.SpoolRow{Key: []byte(proofPrefix + fmt.Sprintf("%020d/%x", node.NodeID, []byte(want.No))), Value: data})
		})
		if err := errors.Join(verifyErr, view.Close()); err != nil {
			return out, fmt.Errorf("verify target node %d plugin settings: %w", node.NodeID, err)
		}
		if actual != expected {
			return out, fmt.Errorf("target node %d plugin settings count mismatch", node.NodeID)
		}
		out.ByTarget[node.NodeID] = actual
		out.Records += actual
	}
	if err := b.flush(); err != nil {
		return out, err
	}
	// Workspace ordering makes the evidence independent of filesystem listing order.
	if err := w.Walk(ctx, []byte(proofPrefix), func(row transfer.SpoolRow) error { return evidence.Encode(row) }); err != nil {
		return out, err
	}
	out.Digest = hex.EncodeToString(hash.Sum(nil))
	return out, nil
}
