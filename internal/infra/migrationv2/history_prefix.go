package migrationv2

import (
	"encoding/binary"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

// DecodeHistoryConfigCommand also identifies valid empty/type-zero management
// commands so a different channel in the same Slot is not falsely implicated.
// This only decodes original identity; default authority/import decoding and
// the separate proof required for empty-channel archival remain unchanged.
func (r Reader) DecodeHistoryConfigCommand(c migration.RawConfigCommand) migration.ChannelConfigLog {
	if out, empty, err := r.DecodeEmptyChannelCommand(c); err == nil && empty {
		return out
	}
	return r.DecodeAuthorityCommand(c)
}

// HistoryLayout follows original wkdb primary grouping: channel configurations
// are in shard zero, while message primaries and their tail share owner%shards.
func (Reader) HistoryLayout(owner uint64, shards int) (migration.HistoryLayout, error) {
	if shards < 1 || shards > 1024 {
		return migration.HistoryLayout{}, errors.New("invalid original history shard count")
	}
	key := func(table uint16, kind byte) []byte {
		b := make([]byte, 12)
		binary.BigEndian.PutUint16(b, table)
		b[2] = kind
		binary.BigEndian.PutUint64(b[4:], owner)
		return b
	}
	return migration.HistoryLayout{ConfigKey: key(0x0b01, 1), MessageShard: int(owner % uint64(shards)), MessagePrefix: key(0x0101, 1), TailKey: key(0x0101, 4)}, nil
}

func (Reader) HistoryMessageKey(owner, sequence uint64) []byte {
	b := make([]byte, 20)
	copy(b, []byte{1, 1, 1, 0})
	binary.BigEndian.PutUint64(b[4:], owner)
	binary.BigEndian.PutUint64(b[12:], sequence)
	return b
}
