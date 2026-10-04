package migration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// validatePluginBinding checks representability before any target publication.
// Preserving an association does not certify its plugin's business behavior.
func validatePluginBinding(p *SourcePluginBinding) error {
	if p.UID == "" || p.PluginNo == "" || len(p.UID) > math.MaxUint16 || len(p.PluginNo) > math.MaxUint16 {
		return errors.New("plugin binding identity exceeds native key limits")
	}
	// Check the original precision so millisecond rounding cannot hide a
	// backwards update. The archive retains both original timestamps exactly.
	if p.UpdatedAtNS < p.CreatedAtNS {
		return errors.New("plugin binding update precedes its creation")
	}
	return nil
}

type pluginBindingGroup struct {
	HashSlot uint16
	PluginNo string
}

func (g pluginBindingGroup) key() string { return tuple(g.HashSlot, g.PluginNo) }

func pluginBindingExpectedPrefix(node uint64, g pluginBindingGroup) string {
	return fmt.Sprintf("verification/plugin-index/%020d/%s/", node, g.key())
}

// verifyPluginBindingIndexes compares the operational reverse lookup to a
// source-derived disk ledger. Each (Hash Slot, plugin) is paged only once per
// node, rather than rescanning all users for every binding.
func verifyPluginBindingIndexes(ctx context.Context, node uint64, w Workspace, view TargetView) error {
	return w.Walk(ctx, []byte("verification/plugin-groups/"), func(row transfer.SpoolRow) error {
		var group pluginBindingGroup
		if err := UnmarshalState(row.Value, &group); err != nil {
			return err
		}
		prefix := pluginBindingExpectedPrefix(node, group)
		var expected, actual uint64
		if err := w.Walk(ctx, []byte(prefix), func(transfer.SpoolRow) error { expected++; return nil }); err != nil {
			return err
		}
		var last string
		err := view.WalkPluginBindings(ctx, group.HashSlot, group.PluginNo, func(p meta.PluginUserBinding) error {
			// Native string keys sort by byte length, then byte content.
			if p.PluginNo != group.PluginNo || targetHashSlot(p.UID) != group.HashSlot || (actual > 0 && (len(p.UID) < len(last) || (len(p.UID) == len(last) && p.UID <= last))) {
				return errors.New("plugin binding reverse index has an invalid identity or order")
			}
			want, found, err := w.Get(ctx, []byte(prefix+tuple(p.UID)))
			if err != nil {
				return err
			}
			got, err := MarshalState(map[string]any{"uid": p.UID, "plugin_no": p.PluginNo, "created_at_ms": p.CreatedAtMS, "updated_at_ms": p.UpdatedAtMS})
			if err != nil {
				return err
			}
			if !found || !bytes.Equal(got, want) {
				return errors.New("plugin binding reverse index differs from selected source")
			}
			actual++
			last = p.UID
			return nil
		})
		if err != nil {
			return err
		}
		if actual != expected {
			return errors.New("plugin binding reverse index count differs from selected source")
		}
		return nil
	})
}
