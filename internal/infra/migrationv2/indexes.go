package migrationv2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

// DescribeIndexes mirrors SourceCommit's operational lookups, not its nominal
// table declarations. In particular Subscriber/Allowlist repeat the first
// index-name byte, and ChannelInfo/time indexes use the unique-index kind with a
// nonunique key. Counters and historical administrative indexes may be sparse
// or stale in healthy original data; they receive shape checks only.
func (Reader) DescribeIndexes(row Row, id RecordIdentity, shards int) (facts migration.SourceIndexFacts, err error) {
	if row.Shard < 0 || row.Shard >= shards {
		return facts, errors.New("source index row is outside shard inventory")
	}
	if counters, err := channelCountersOnly(row); counters || err != nil {
		if err != nil {
			return facts, err
		}
		if row.Shard != int(row.ID%uint64(shards)) {
			return facts, errors.New("original channel counter is in the wrong shard")
		}
		return facts, nil
	}
	if isLegacyStream(row) {
		name, err := decodeLegacyStream(row)
		if err != nil {
			return facts, err
		}
		h := fnv.New32()
		_, _ = h.Write([]byte(name))
		if int(h.Sum32()%uint32(shards)) != row.Shard {
			return facts, errors.New("legacy stream record is in the wrong original shard")
		}
		return facts, nil
	}
	if row.Kind == Index || row.Kind == SecondaryIndex {
		return describeStoredIndex(row)
	}
	if row.Table == "PendingConversation" {
		return facts, nil
	}
	expectedShard := -1
	switch row.Table {
	case "User", "Device", "Conversation":
		if id.UID != "" {
			h := fnv.New32()
			_, _ = h.Write([]byte(id.UID))
			expectedShard = int(h.Sum32() % uint32(shards))
		}
	case "Message", "Subscriber", "Allowlist", "Denylist", "ChannelInfo", "MessageEventState", "MessageEventSeq":
		if id.Channel.ID != "" {
			expectedShard = int(channelHash(id.Channel.ID, id.Channel.Type) % uint64(shards))
		}
	case "ChannelClusterConfig", "SystemUid", "PluginUser":
		expectedShard = 0
	}
	if expectedShard >= 0 && expectedShard != row.Shard {
		return facts, fmt.Errorf("source %s index primary is in the wrong original shard", row.Table)
	}
	if row.Kind != Primary {
		return facts, nil
	}
	add := func(table uint16, kind Kind, name uint16, value []byte, parts ...uint64) {
		facts.Expected = append(facts.Expected, migration.SourceIndexEntry{Key: originalIndexKey(table, kind, name, parts...), Value: bytes.Clone(value)})
	}
	switch row.Table {
	case "Device":
		add(0x0301, SecondaryIndex, 0x0301, nil, stringHash(id.UID), row.ID)
	case "PluginUser":
		// Both directions are live original lookups. The legacy writer uses
		// Index kind even though its method calls them second indexes.
		add(0x1601, Index, 0x1601, nil, stringHash(id.UID), row.ID)
		add(0x1601, Index, 0x1602, nil, stringHash(string(row.Fields["PluginNo"])), row.ID)
	case "Subscriber":
		add(0x0401, Index, 0x0404, uint64Bytes(row.ID), row.Owner, row.ID)
	case "Allowlist":
		add(0x0801, Index, 0x0808, uint64Bytes(row.ID), row.Owner, row.ID)
	case "Denylist":
		add(0x0701, Index, 0x0701, uint64Bytes(row.ID), row.Owner, row.ID)
	case "Conversation":
		add(0x0901, Index, 0x0901, uint64Bytes(row.ID), row.Owner, channelHash(id.Channel.ID, id.Channel.Type))
	case "Message":
		messageID, err := scalar64(row, "MessageId")
		if err != nil {
			return facts, err
		}
		primary := append(uint64Bytes(row.Owner), uint64Bytes(row.ID)...)
		add(0x0101, Index, 0x0101, primary, messageID)
		facts.Expected[len(facts.Expected)-1].LatestMessageSeq = row.ID
		add(0x0101, SecondaryIndex, 0x0101, nil, stringHash(string(row.Fields["FromUid"])), row.Owner, row.ID)
		sender := &facts.Expected[len(facts.Expected)-1]
		sender.SenderKey = bytes.Clone(sender.Key[:22])
		sender.SenderSeq = row.ID
		add(0x0101, SecondaryIndex, 0x0102, nil, stringHash(string(row.Fields["ClientMsgNo"])), row.Owner, row.ID)
	}
	return facts, nil
}

func describeStoredIndex(row Row) (facts migration.SourceIndexFacts, err error) {
	if len(row.Key) < 6 || row.Key[2] != byte(row.Kind) || row.Key[3] != 0 {
		return facts, errors.New("invalid source index prefix")
	}
	table := binary.BigEndian.Uint16(row.Key)
	schema, ok := tables[table]
	if !ok || schema.name != row.Table {
		return facts, errors.New("source index table identity mismatch")
	}
	name := binary.BigEndian.Uint16(row.Key[4:6])
	keySize, valueSize := 0, 0
	operational, allowAbsent, unique := false, false, false
	var primary []byte
	switch row.Table {
	case "Message":
		switch {
		case row.Kind == Index && name == 0x0101:
			keySize, valueSize, operational, allowAbsent, unique = 14, 16, true, true, true
		case row.Kind == SecondaryIndex && (name == 0x0101 || name == 0x0102):
			keySize, operational = 30, true
			allowAbsent = name == 0x0102
		case row.Kind == Index && name == 0x0103:
			keySize = 30 // administrative timestamp index
		}
	case "Device":
		if row.Kind == SecondaryIndex && name >= 0x0301 && name <= 0x0305 {
			keySize = 22
			operational = name == 0x0301
		}
	case "Subscriber", "Allowlist", "Denylist":
		indexName := table
		if row.Table == "Subscriber" {
			indexName = 0x0404
		}
		if row.Table == "Allowlist" {
			indexName = 0x0808
		}
		if row.Kind == Index && name == indexName {
			keySize, valueSize, operational = 22, 8, true
		}
		if row.Kind == SecondaryIndex && (name == table || name == table+1) {
			keySize = 30
		}
	case "Conversation":
		if row.Kind == Index && name == 0x0901 {
			keySize, valueSize, operational = 22, 8, true
		}
		if row.Kind == SecondaryIndex && len(row.Key) >= 14 {
			name = binary.BigEndian.Uint16(row.Key[12:14])
			if name >= 0x0901 && name <= 0x0903 {
				keySize = 30
			}
		}
	case "User":
		if row.Kind == Index && name == 0x0201 {
			keySize, valueSize = 14, 8
		}
		if row.Kind == SecondaryIndex && (name == 0x0201 || name == 0x0202) {
			keySize = 22
		}
	case "ChannelInfo":
		if row.Kind == Index && name == 0x0601 && len(row.Key) == 14 {
			keySize, valueSize = 14, 8
		} else if row.Kind == Index && name >= 0x0601 && name <= 0x0609 {
			keySize = 22
		}
	case "ChannelClusterConfig":
		if row.Kind == Index && name == 0 {
			keySize, valueSize = 14, 8
		} // original declaration leaves this name zero
		if row.Kind == SecondaryIndex && name >= 0x0b01 && name <= 0x0b03 {
			keySize = 22
		}
	case "PluginUser":
		if row.Kind == Index && (name == 0x1601 || name == 0x1602) {
			keySize, operational = 22, true
		}
	}
	if keySize == 0 || len(row.Key) != keySize || len(row.Value) != valueSize {
		return facts, fmt.Errorf("invalid or unsupported source %s index shape/type", row.Table)
	}
	if !operational {
		return facts, nil
	}
	if row.Table == "Message" {
		ref := row.Value
		if row.Kind == SecondaryIndex {
			ref = row.Key[14:30]
		}
		if binary.BigEndian.Uint64(ref[8:]) == 0 {
			return facts, errors.New("source message index has a zero sequence")
		}
		primary = append([]byte{0x01, 0x01, byte(Primary), 0}, ref...)
	}
	if row.Table == "PluginUser" {
		primary = append([]byte{0x16, 0x01, byte(Primary), 0}, row.Key[14:22]...)
	}
	if row.Table == "Conversation" {
		if binary.BigEndian.Uint64(row.Value) == 0 {
			return facts, errors.New("source conversation index has a zero primary ID")
		}
		primary = append([]byte{0x09, 0x01, byte(Primary), 0}, row.Key[6:14]...)
		primary = append(primary, row.Value...)
	}
	facts.Actual = &migration.SourceIndexEntry{Key: bytes.Clone(row.Key), Value: bytes.Clone(row.Value), PrimaryKey: primary, AllowAbsentPrimary: allowAbsent, NodeUnique: unique}
	if row.Table == "Message" && row.Kind == SecondaryIndex && name == 0x0101 {
		facts.Actual.SenderKey = bytes.Clone(row.Key[:22])
		facts.Actual.SenderSeq = binary.BigEndian.Uint64(row.Key[22:])
	}
	return facts, nil
}

func originalIndexKey(table uint16, kind Kind, name uint16, parts ...uint64) []byte {
	key := make([]byte, 6+8*len(parts))
	binary.BigEndian.PutUint16(key, table)
	key[2] = byte(kind)
	binary.BigEndian.PutUint16(key[4:], name)
	for i, value := range parts {
		binary.BigEndian.PutUint64(key[6+i*8:], value)
	}
	return key
}
