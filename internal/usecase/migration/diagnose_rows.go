package migration

import (
	"fmt"
	"math"
	"strings"
)

type diagnosticLogRow struct {
	Ref  DiagnosticFinding `json:"ref"`
	Seq  uint64            `json:"seq"`
	Tail bool              `json:"tail"`
}

type diagnosticIndexRow struct {
	Ref   DiagnosticFinding `json:"ref"`
	Entry SourceIndexEntry  `json:"entry"`
}

func (d *diagnostician) row(node uint64, shards int, row Row, exclusions *Exclusions) error {
	id, identifyErr := d.decoder.Identify(row)
	if identifyErr != nil {
		return d.issue(row.Table+".identity", node, row)
	}
	id, resolveErr := resolveIdentity(d.ctx, d.w, id)
	if resolveErr != nil {
		if err := d.issue(row.Table+".unresolved_identity", node, row); err != nil {
			return err
		}
		// Stored indexes can still be shape checked without a logical identity.
		if row.Kind != Index && row.Kind != SecondaryIndex {
			return nil
		}
	}
	indexes, err := d.decoder.DescribeIndexes(row, id, shards)
	if err != nil {
		if e := d.issue(row.Table+".index_shape_or_placement", node, row); e != nil {
			return e
		}
	} else {
		for _, entry := range indexes.Expected {
			key := fmt.Sprintf("diagnostic/index/%020d/%04d/%x/e/%s", node, row.Shard, entry.Key, diagnosticRef(node, row).KeySHA256)
			if err := d.put(key, diagnosticIndexRow{Ref: diagnosticRef(node, row), Entry: entry}); err != nil {
				return err
			}

			if len(entry.SenderKey) > 0 {
				if err := d.put(fmt.Sprintf("diagnostic/sender/%020d/%04d/%x/%020d", node, row.Shard, entry.SenderKey, entry.SenderSeq), entry.SenderSeq); err != nil {
					return err
				}
			}
		}
		if entry := indexes.Actual; entry != nil {
			if err := d.put(fmt.Sprintf("diagnostic/index/%020d/%04d/%x/a", node, row.Shard, entry.Key), diagnosticIndexRow{Ref: diagnosticRef(node, row), Entry: *entry}); err != nil {
				return err
			}
			if entry.NodeUnique {
				if err := d.unique("message_index_across_shards", node, string(entry.Key), row); err != nil {
					return err
				}
			}
		}
	}
	category, reason, err := sourceCategory(row, exclusions)
	if err != nil {
		return d.issue(row.Table+".unsupported_source_category", node, row)
	}
	if reason != "" {
		if reason == "excluded_legacy_stream_storage" {
			return d.issue("legacy.stream_storage_excluded", node, row)
		}
		if row.Table == "Plugin" && row.Kind == Primary {
			desc, err := d.decoder.Describe(row, id)
			if err != nil {
				return d.issue("plugin.decode", node, row)
			}
			if desc.Plugin == nil || len(desc.Plugin.Methods) != 0 || desc.Plugin.HasConfig {
				finding := diagnosticRef(node, row)
				finding.Code = "plugin.business_mapping"
				if desc.Plugin != nil {
					finding.Plugin = &desc.Plugin.Evidence
				}
				return d.emit(finding)
			}
		}
		if reason == "old_management_data" {
			return d.issue("management.archived", node, row)
		}
		return nil
	}
	if category == "" {
		return nil
	}
	description, err := d.decoder.Describe(row, id)
	if err != nil {
		return d.issue(row.Table+".description", node, row)
	}
	if description.DerivedChannelCounters {
		return nil
	}
	if err := d.unique("logical_"+row.Table, node, description.Key, row); err != nil {
		return err
	}
	if strings.HasPrefix(id.Channel.ID, "__wk_internal_memberlist__/") || id.Channel.ID == "__wk_internal_system_uids__" {
		if err := d.issue("channel.reserved_namespace", node, row); err != nil {
			return err
		}
	}
	if row.Table == "ChannelClusterConfig" || row.Table == "SystemUid" {
		return nil
	}
	facts, err := d.decoder.DecodeBusiness(row, id)
	if err != nil {
		return d.issue(row.Table+".business_decode", node, row)
	}
	prefix := fmt.Sprintf("diagnostic/business/%020d/", node)
	ref := diagnosticRef(node, row)
	switch {
	case facts.Message != nil:
		m := facts.Message
		for _, field := range UnsupportedMessageFields(*m) {
			if err := d.issue("message.field."+field, node, row); err != nil {
				return err
			}
		}
		// The shared encoder also validates the native recovery byte budget.
		if _, _, err := encodeRecoveryMessage(*m); err != nil {
			if e := d.issue("message.native_record", node, row); e != nil {
				return e
			}
		}
		if err := d.unique("message_id", node, fmt.Sprint(m.MessageID), row); err != nil {
			return err
		}
		if m.ClientMsgNo != "" {
			if err := d.unique("idempotency", node, tuple(id.Channel.ID, id.Channel.Type, m.FromUID, m.ClientMsgNo), row); err != nil {
				return err
			}
		}
		return d.put(prefix+"log/"+channelTuple(id.Channel)+fmt.Sprintf("/m/%020d/%s", m.MessageSeq, ref.KeySHA256), diagnosticLogRow{Ref: ref, Seq: m.MessageSeq})
	case facts.Tail != nil:
		return d.put(prefix+"log/"+channelTuple(id.Channel)+"/t/"+ref.KeySHA256, diagnosticLogRow{Ref: ref, Seq: facts.Tail.LastSeq, Tail: true})
	case facts.Conversation != nil:
		c := facts.Conversation
		if c.UnreadCount != 0 {
			if err := d.issue("conversation.unread_count", node, row); err != nil {
				return err
			}
		}
		if c.Type == 1 && c.DeletedToSeq != 0 {
			if err := d.issue("conversation.command_delete_boundary", node, row); err != nil {
				return err
			}
		}
		return d.put(prefix+"conversation/"+tuple(c.UID, c.Channel.ID, c.Channel.Type), true)
	case facts.Member != nil:
		if row.Table == "Subscriber" {
			return d.put(prefix+"subscriber/"+tuple(id.UID, id.Channel.ID, id.Channel.Type)+"/"+ref.KeySHA256, struct {
				Ref    DiagnosticFinding
				Member SourceMember
			}{ref, *facts.Member})
		}
	case facts.Device != nil:
		if facts.Device.Flag > math.MaxInt64 {
			if err := d.issue("device.flag_range", node, row); err != nil {
				return err
			}
		}
		return d.unique("device_key", node, tuple(facts.Device.UID, facts.Device.Flag), row)
	case facts.PluginBinding != nil:
		if err := validatePluginBinding(facts.PluginBinding); err != nil {
			return d.issue("plugin.binding_native_fields", node, row)
		}
		return d.unique("plugin_binding", node, tuple(facts.PluginBinding.UID, facts.PluginBinding.PluginNo), row)
	case facts.User != nil:
		if facts.User.PluginNo != "" {
			return d.issue("plugin.legacy_user_binding", node, row)
		}
	case facts.EventState != nil:
		state := facts.EventState
		if err := d.unique("event_id", node, tuple(id.Channel.ID, id.Channel.Type, id.ClientMsgNo, state.LastEventID), row); err != nil {
			return err
		}
		return d.put(prefix+"event/state/"+tuple(id.Channel.ID, id.Channel.Type, id.ClientMsgNo)+"/"+ref.KeySHA256, struct {
			Ref   DiagnosticFinding
			Facts BusinessFacts
		}{ref, facts})
	case facts.EventCursor != nil:
		return d.put(prefix+"event/cursor/"+tuple(id.Channel.ID, id.Channel.Type, id.ClientMsgNo)+"/"+ref.KeySHA256, struct {
			Ref   DiagnosticFinding
			Facts BusinessFacts
		}{ref, facts})
	}
	return nil
}
