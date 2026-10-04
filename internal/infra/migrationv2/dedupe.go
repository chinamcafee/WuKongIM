package migrationv2

import (
	"encoding/json"
	"errors"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"strings"
)

// InspectDedupeMessage hashes original fields without requiring unsupported
// legacy flags to be convertible. Such compatibility checks still run later.
func (Reader) InspectDedupeMessage(row Row, shards int) (out migration.DedupeMessage, err error) {
	m, term, err := DecodeMessage(row)
	if err != nil {
		return out, err
	}
	if shards < 1 || row.Shard != int(row.Owner%uint64(shards)) {
		return out, errors.New("message in wrong original shard")
	}
	data, err := json.Marshal(row.Fields)
	if err != nil {
		return out, err
	}
	out.UnsupportedFields = migration.UnsupportedMessageFields(m)
	out.Owner = row.Owner
	out.ChannelSHA256 = evidenceSHA([]byte(migration.IdentityKey(m.ChannelID, m.ChannelType)))
	if m.FromUID != "" && m.ClientMsgNo != "" {
		out.ClientKeySHA256 = evidenceSHA([]byte(migration.IdentityKey(m.ChannelID, m.ChannelType, m.FromUID, m.ClientMsgNo)))
	}
	out.CMD = isCMDMessage(m)
	out.Stream = migration.IsStreamMessage(m)
	out.StreamParent = len(row.Fields["StreamNo"]) != 0
	out.MessageEvidence = migration.MessageEvidence{ID: m.MessageID, Sequence: m.MessageSeq, Term: term, SHA256: evidenceSHA(data)}
	return out, nil
}

// isCMDMessage follows original default command routing, including online CMD.
func isCMDMessage(m channelcompat.Message) bool {
	return m.Framer.SyncOnce || strings.HasSuffix(m.ChannelID, "____cmd") || m.ChannelID == "systemcmdonline"
}
