package migrationv2

import (
	"encoding/binary"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

func emptyChannelPrimary(r Row) bool {
	id, present := r.Fields["ChannelId"]
	return present && len(id) == 0 && len(r.Fields["ChannelType"]) == 1 && r.Fields["ChannelType"][0] == 0 && r.Kind == Primary && (r.Table == "ChannelInfo" || r.Table == "ChannelClusterConfig")
}

// InspectEmptyChannel recognizes only the fully identified empty/type-zero
// administrative pair. Sparse permission counters are handled separately.
func (Reader) InspectEmptyChannel(r Row, shards int) (*migration.EmptyChannelRow, error) {
	if !emptyChannelPrimary(r) {
		return nil, nil
	}
	if shards < 1 {
		return nil, errors.New("missing empty-channel shard count")
	}
	table := uint16(0x0601)
	shard := int(channelHash("", 0) % uint64(shards))
	if r.Table == "ChannelClusterConfig" {
		table = 0x0b01
		shard = 0
	}
	if shards < 1 || len(r.Key) != 12 || binary.BigEndian.Uint16(r.Key) != table || r.Key[2] != byte(Primary) || r.Key[3] != 0 || binary.BigEndian.Uint64(r.Key[4:]) != channelHash("", 0) || r.ID != channelHash("", 0) || r.Owner != 0 || r.Shard != shard || len(r.Value) != 0 {
		return nil, errors.New("invalid empty-channel primary placement")
	}
	schema := tables[table]
	for name, v := range r.Fields {
		known := false
		for _, col := range schema.columns {
			if col.name == name {
				known = true
				if (col.size > 0 && len(v) != col.size) || (col.size < 0 && len(v)%(-col.size) != 0) {
					return nil, errors.New("invalid empty-channel scalar")
				}
				break
			}
		}
		if !known {
			return nil, errors.New("unknown empty-channel field")
		}
	}
	out := &migration.EmptyChannelRow{}
	if r.Table == "ChannelInfo" {
		for name, v := range r.Fields {
			switch name {
			case "ChannelId", "ChannelType", "CreatedAt", "UpdatedAt":
				continue
			case "Ban", "Large", "Disband", "SendBan", "AllowStranger", "SubscriberCount", "AllowlistCount", "DenylistCount":
				for _, b := range v {
					if b != 0 {
						return nil, errors.New("empty channel has business policy or membership count")
					}
				}
			default:
				return nil, errors.New("unsupported empty-channel body")
			}
		}
	} else {
		c, err := inspectChannelConfig(r, true)
		if err != nil {
			return nil, err
		}
		if c.Status != 0 || c.MigrateFrom != 0 || c.MigrateTo != 0 || len(c.Learners) != 0 {
			return nil, errors.New("empty channel has unfinished authority changes")
		}
		if len(r.Fields["Version"]) != 2 || binary.BigEndian.Uint16(r.Fields["Version"]) != 1 {
			return nil, errors.New("unsupported empty-channel config version")
		}
		out.Config = &c
	}
	return out, nil
}

// EmptyChannelReference checks every supported original reference layout. A
// hash-only dangling reference is a blocker even if its primary was deleted.
func (Reader) EmptyChannelReference(r Row) (bool, error) {
	owner, event := channelHash("", 0), eventChannelHash("", 0)
	at := func(b []byte, offset int, want uint64) bool {
		return len(b) >= offset+8 && binary.BigEndian.Uint64(b[offset:]) == want
	}
	if id, ok := r.Fields["ChannelId"]; ok {
		typ := r.Fields["ChannelType"]
		if len(typ) != 1 {
			return false, errors.New("incomplete channel reference")
		}
		if channelHash(string(id), typ[0]) == owner || eventChannelHash(string(id), typ[0]) == event {
			return true, nil
		}
	}
	switch r.Table {
	case "Message":
		if r.Kind == Primary || r.Kind == Other {
			return at(r.Key, 4, owner), nil
		}
		if r.Kind == Index && len(r.Key) == 14 {
			return at(r.Value, 0, owner), nil
		}
		return at(r.Key, 14, owner), nil
	case "Subscriber", "Allowlist", "Denylist":
		if r.Kind == Primary {
			return r.Owner == owner || at(r.Key, 4, owner), nil
		}
		return at(r.Key, 6, owner), nil
	case "Conversation":
		if r.Kind == Index {
			return at(r.Key, 14, owner), nil
		}
		return false, nil
	case "PendingConversation":
		return false, nil // explicit identity checked above
	case "ConversationLocalUser", "LeaderTermSequence", "ChannelCommon":
		return at(r.Key, 4, owner), nil
	case "MessageEvent", "MessageEventState", "MessageEventSeq":
		return at(r.Key, 4, event), nil
	case "SubscriberChannelRelation", "MessageNotifyQueue":
		return false, errors.New("opaque legacy reference prevents empty-channel absence proof")
	case "ChannelInfo", "ChannelClusterConfig":
		// Administrative indexes of the certified pair remain raw archival data.
		return r.Kind == Primary && r.ID == owner, nil
	case "User", "Device", "SystemUid", "PluginUser", "Plugin", "Tester", "Total", "IgnoredConversation", "LegacyStream", "LegacyStreamMeta":
		return false, nil
	default:
		return false, errors.New("unknown table prevents empty-channel absence proof")
	}
}

// DecodeEmptyChannelCommand still consumes the complete original command and
// validates outer/inner identities. Other malformed commands remain errors.
func (Reader) DecodeEmptyChannelCommand(c migration.RawConfigCommand) (migration.ChannelConfigLog, bool, error) {
	out, relevant, err := decodeConfigLogWithEmpty(c.Slot, c.Index, c.Term, c.Data, true)
	if err != nil {
		return out, false, err
	}
	return out, relevant && out.Config.Owner == channelHash("", 0) && out.Config.IdentitySHA256 == evidenceSHA([]byte(migration.IdentityKey("", uint8(0)))), nil
}
