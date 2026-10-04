package migrationv2

import (
	"encoding/binary"
	"errors"
)

// channelCountersOnly recognizes the original independent member-count writes.
// With neither identity column, GetChannel returns an empty body. These derived
// counts do not grant permissions; the actual member rows must still be joined,
// checked against their operational indexes and selected by source authority.
func channelCountersOnly(row Row) (bool, error) {
	if row.Table != "ChannelInfo" || row.Kind != Primary || len(row.Fields) == 0 {
		return false, nil
	}
	for name := range row.Fields {
		switch name {
		case "SubscriberCount", "AllowlistCount", "DenylistCount":
		default:
			return false, nil
		}
	}
	if len(row.Key) != 12 || binary.BigEndian.Uint32(row.Key[:4]) != 0x06010100 ||
		binary.BigEndian.Uint64(row.Key[4:]) != row.ID || row.ID == 0 || row.Owner != 0 || len(row.Value) != 0 {
		return false, errors.New("invalid original channel counter key")
	}
	for _, value := range row.Fields {
		if len(value) != 4 {
			return false, errors.New("invalid original channel counter scalar")
		}
	}
	return true, nil
}
