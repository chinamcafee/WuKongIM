package migrationv2

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// DecodeMessage translates one complete original primary row and checks its
// repeated identities. The source term is returned as provenance; it is not a
// v3 consensus term. Secondary indexes never supply missing primary fields.
func DecodeMessage(row Row) (channelcompat.Message, uint64, error) {
	if row.Table != "Message" || row.Kind != Primary || len(row.Key) != 20 {
		return channelcompat.Message{}, 0, errors.New("expected a v2 message primary row")
	}
	var decodeErr error
	number := func(field string, width int, required bool) uint64 {
		value, present := row.Fields[field]
		if !present && !required {
			return 0
		}
		if len(value) != width {
			if decodeErr == nil {
				decodeErr = fmt.Errorf("Message.%s: missing or invalid scalar", field)
			}
			return 0
		}
		var result uint64
		for _, b := range value {
			result = result<<8 | uint64(b)
		}
		return result
	}
	id := number("MessageId", 8, true)
	seq := number("MessageSeq", 8, true)
	typ := uint8(number("ChannelType", 1, true))
	header := uint8(number("Header", 1, false))
	msg := channelcompat.Message{
		MessageID: id, MessageSeq: seq, ChannelID: string(row.Fields["ChannelId"]), ChannelType: typ,
		Framer:  frame.Framer{NoPersist: header&1 != 0, RedDot: header&2 != 0, SyncOnce: header&4 != 0, DUP: header&8 != 0},
		Setting: frame.Setting(number("Setting", 1, false)), Expire: uint32(number("Expire", 4, false)),
		Timestamp: int32(number("Timestamp", 4, false)), ClientMsgNo: string(row.Fields["ClientMsgNo"]),
		StreamNo: string(row.Fields["StreamNo"]), Topic: string(row.Fields["Topic"]), FromUID: string(row.Fields["FromUid"]),
		Payload: bytes.Clone(row.Fields["Payload"]),
	}
	term := number("Term", 8, false)
	if decodeErr != nil {
		return channelcompat.Message{}, 0, decodeErr
	}
	if int64(id) <= 0 || seq == 0 || seq != row.ID || binary.BigEndian.Uint64(row.Key[12:20]) != seq || msg.ChannelID == "" || typ == 0 {
		return channelcompat.Message{}, 0, errors.New("v2 message identities are missing or inconsistent")
	}
	if _, ok := row.Fields["Payload"]; !ok {
		return channelcompat.Message{}, 0, errors.New("v2 message payload is missing")
	}
	hash := channelHash(msg.ChannelID, typ)
	if row.Owner != hash || binary.BigEndian.Uint64(row.Key[4:12]) != hash {
		return channelcompat.Message{}, 0, errors.New("v2 message channel identity does not match its key")
	}
	if header>>4 != 0 && frame.FrameType(header>>4) != frame.RECV {
		return channelcompat.Message{}, 0, errors.New("v2 message contains a non-message frame type")
	}
	msg.ServerTimestampMS = int64(msg.Timestamp) * 1000
	return msg, term, nil
}

func channelHash(id string, typ uint8) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id + "-" + strconv.FormatUint(uint64(typ), 10)))
	return h.Sum64()
}
