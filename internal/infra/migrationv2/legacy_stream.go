package migrationv2

import (
	"encoding/binary"
	"errors"
	"unicode/utf8"
)

func isLegacyStream(row Row) bool {
	return row.Table == "LegacyStream" || row.Table == "LegacyStreamMeta"
}

// decodeLegacyStream validates the historical writer at 9fc693d5068b20ec6be3e3bc35925b38a277feac.
// The table was removed before SourceCommit, but old files can retain its rows.
// It returns only the original sharding identity; no omitted content is converted.
func decodeLegacyStream(row Row) (string, error) {
	k := row.Key
	if len(k) < 4 || k[3] != 0 || len(row.Fields) != 0 || row.ID != 0 || row.Owner != 0 {
		return "", errors.New("invalid legacy stream record")
	}
	chunk := row.Table == "LegacyStream"
	if chunk {
		if len(k) != 22 || binary.BigEndian.Uint16(k) != 0x1101 || row.Kind != Index || k[2] != byte(Index) || binary.BigEndian.Uint16(k[4:6]) != 0x1101 {
			return "", errors.New("invalid legacy stream chunk key")
		}
	} else if row.Table != "LegacyStreamMeta" || len(k) != 12 || binary.BigEndian.Uint16(k) != 0x1201 || row.Kind != Primary || k[2] != byte(Primary) {
		return "", errors.New("invalid legacy stream metadata key")
	}
	c := legacyStreamDecoder{data: row.Value}
	if c.number(2) != 0 || c.invalid {
		return "", errors.New("unsupported or truncated legacy stream version")
	}
	var sequence uint64
	if chunk {
		sequence = c.number(8)
	}
	name := c.text()
	if name == "" {
		c.invalid = true
	}
	if chunk {
		if c.invalid || binary.BigEndian.Uint64(k[6:14]) != stringHash(name) || binary.BigEndian.Uint64(k[14:]) != sequence {
			return "", errors.New("legacy stream chunk encoding or key identity mismatch")
		}
		// Remaining bytes are opaque original payload, including an empty chunk.
	} else {
		c.text()    // ChannelId
		c.number(1) // ChannelType
		c.text()    // FromUid
		c.text()    // ClientMsgNo
		c.number(8) // MessageId
		c.number(8) // MessageSeq
		if c.invalid || len(c.data) != 0 || binary.BigEndian.Uint64(k[4:]) != stringHash(name) {
			return "", errors.New("legacy stream metadata encoding or key identity mismatch")
		}
	}
	return name, nil
}

// legacyStreamDecoder bounds every read and never includes source bytes in errors.
type legacyStreamDecoder struct {
	data    []byte
	invalid bool
}

func (c *legacyStreamDecoder) number(n int) uint64 {
	if len(c.data) < n {
		c.invalid = true
		return 0
	}
	var v uint64
	for _, b := range c.data[:n] {
		v = v<<8 | uint64(b)
	}
	c.data = c.data[n:]
	return v
}

func (c *legacyStreamDecoder) text() string {
	n := int(c.number(2))
	// Original WKProto strings use a signed int16 length.
	if c.invalid || n > 32767 || len(c.data) < n {
		c.invalid = true
		return ""
	}
	b := c.data[:n]
	c.data = c.data[n:]
	if !utf8.Valid(b) {
		c.invalid = true
		return ""
	}
	return string(b)
}
