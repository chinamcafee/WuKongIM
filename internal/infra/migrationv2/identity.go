package migrationv2

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

type RecordIdentity = migration.RecordIdentity

func (Reader) Identify(row Row) (RecordIdentity, error) { return Identify(row) }

// Identify validates repeated identities and retains unresolved hash references
// for a later disk-backed join. It does not infer ownership from a row's node.
func Identify(row Row) (id RecordIdentity, err error) {
	if counters, err := channelCountersOnly(row); counters || err != nil {
		return id, err
	}
	if row.Table == "IgnoredConversation" {
		return id, validateIgnoredConversation(row)
	}
	if isLegacyStream(row) {
		_, err := decodeLegacyStream(row)
		return id, err
	}
	if row.Kind == Other {
		switch row.Table {
		case "Message":
			if len(row.Key) != 12 || len(row.Value) != 16 {
				return id, errors.New("invalid original message tail")
			}
			id.ChannelHash = binary.BigEndian.Uint64(row.Key[4:12])
		case "MessageEventSeq":
			if len(row.Key) != 20 || len(row.Value) != 8 {
				return id, errors.New("invalid original event cursor")
			}
			id.EventChannelHash = binary.BigEndian.Uint64(row.Key[4:12])
			id.ClientMsgHash = binary.BigEndian.Uint64(row.Key[12:20])
		}
		return id, nil
	}
	if row.Kind != Primary {
		return id, nil
	}
	for _, name := range []string{"Uid", "ChannelId", "FromUid", "ClientMsgNo", "PluginNo", "StreamNo", "Topic"} {
		if v, ok := row.Fields[name]; ok && !utf8.Valid(v) {
			// These original column/index writers and native membership keys use
			// exact bytes. Internal migration state has a lossless string codec;
			// JSON/protocol fields elsewhere still need their own compatibility proof.
			if (row.Table == "Conversation" && (name == "Uid" || name == "ChannelId")) ||
				(row.Table == "ChannelClusterConfig" && name == "ChannelId") {
				continue
			}
			return id, fmt.Errorf("%s.%s contains invalid UTF-8", row.Table, name)
		}
	}
	id.UID = string(row.Fields["Uid"])
	if id.UID != "" {
		id.UIDHash = stringHash(id.UID)
	}
	if v, ok := row.Fields["ChannelId"]; ok {
		typ := row.Fields["ChannelType"]
		// Original control configuration reads accept type zero. Keep that
		// identity for source Slot comparison; it is not a target placement or
		// permission to import zero-type messages, policies or conversations.
		if len(v) == 0 || len(typ) != 1 || (typ[0] == 0 && row.Table != "ChannelClusterConfig") {
			return id, fmt.Errorf("%s has invalid channel identity", row.Table)
		}
		id.Channel = migration.ChannelIdentity{ID: string(v), Type: typ[0]}
		id.ChannelHash = channelHash(id.Channel.ID, id.Channel.Type)
		id.EventChannelHash = eventChannelHash(id.Channel.ID, id.Channel.Type)
	}
	id.ClientMsgNo = string(row.Fields["ClientMsgNo"])
	if id.ClientMsgNo != "" {
		id.ClientMsgHash = stringHash(id.ClientMsgNo)
	}
	requireUID := func() error {
		if id.UID == "" {
			return fmt.Errorf("%s is missing UID", row.Table)
		}
		return nil
	}
	requireChannel := func() error {
		if id.Channel.ID == "" {
			return fmt.Errorf("%s is missing channel identity", row.Table)
		}
		return nil
	}
	switch row.Table {
	case "User":
		if id.UID == "" {
			// Old sparse user/plugin columns may survive without a user body. Resolve
			// the UID from independently identified rows before assigning semantics.
			id.UIDHash = row.ID
			return id, nil
		}
		if row.ID != id.UIDHash {
			return id, errors.New("v2 user identity does not match its key")
		}
		id.UIDPersonalChannelHash = channelHash(id.UID, 1)
	case "SystemUid":
		if err := requireUID(); err != nil {
			return id, err
		}
		if row.ID != id.UIDHash {
			return id, errors.New("v2 system UID identity does not match its key")
		}
	case "Device", "PluginUser":
		if err := requireUID(); err != nil {
			return id, err
		}
		if row.Table == "Device" {
			// Original personal permissions are addressed by (recipient UID, 1),
			// even when no ChannelInfo body has ever been created for the user.
			id.UIDPersonalChannelHash = channelHash(id.UID, 1)
		} else {
			plugin := string(row.Fields["PluginNo"])
			if plugin == "" || row.ID != stringHash(plugin+"_"+id.UID) {
				return id, errors.New("v2 plugin binding identity does not match its key")
			}
		}
	case "ChannelInfo", "ChannelClusterConfig":
		if err := requireChannel(); err != nil {
			return id, err
		}
		if row.ID != id.ChannelHash {
			return id, errors.New("v2 channel identity does not match its key")
		}
		if row.Table == "ChannelInfo" {
			for _, name := range []string{"Ban", "Disband", "Large", "SendBan", "AllowStranger"} {
				v, ok := row.Fields[name]
				if ok && (len(v) != 1 || v[0] > 1) {
					return id, fmt.Errorf("invalid v2 channel flag %s", name)
				}
			}
		}
	case "Subscriber", "Allowlist", "Denylist":
		if err := requireUID(); err != nil {
			return id, err
		}
		if row.ID != id.UIDHash {
			return id, errors.New("v2 member identity does not match its key")
		}
		id.ChannelHash = row.Owner
	case "Conversation":
		if err := requireUID(); err != nil {
			return id, err
		}
		if err := requireChannel(); err != nil {
			return id, err
		}
		if row.Owner != id.UIDHash {
			return id, errors.New("v2 conversation UID does not match its key")
		}
	case "PendingConversation":
		if err := requireUID(); err != nil {
			return id, err
		}
		if err := requireChannel(); err != nil {
			return id, err
		}
	case "Message":
		// Decode checks the complete scalar shape, sequence, message ID and header.
		// The short-lived payload copy is bounded by the source reader's row limit.
		_, _, err := DecodeMessage(row)
		if err != nil {
			return id, err
		}
		id.UID = string(row.Fields["FromUid"])
		if id.UID != "" {
			id.UIDHash = stringHash(id.UID)
		}
	case "MessageEventState":
		var state struct {
			ChannelID   string `json:"channel_id"`
			ChannelType uint8  `json:"channel_type"`
			ClientMsgNo string `json:"client_msg_no"`
			EventKey    string `json:"event_key"`
		}
		if !utf8.Valid(row.Value) {
			return id, errors.New("invalid UTF-8 in v2 event state")
		}
		if err := json.Unmarshal(row.Value, &state); err != nil {
			return id, err
		}
		if len(row.Key) != 28 || state.ChannelID == "" || state.ChannelType == 0 || state.ClientMsgNo == "" || state.EventKey == "" {
			return id, errors.New("invalid v2 event state identity")
		}
		id.Channel = migration.ChannelIdentity{ID: state.ChannelID, Type: state.ChannelType}
		id.ChannelHash = channelHash(state.ChannelID, state.ChannelType)
		id.EventChannelHash = eventChannelHash(state.ChannelID, state.ChannelType)
		id.ClientMsgNo = state.ClientMsgNo
		id.ClientMsgHash = stringHash(state.ClientMsgNo)
		if binary.BigEndian.Uint64(row.Key[4:12]) != id.EventChannelHash || binary.BigEndian.Uint64(row.Key[12:20]) != id.ClientMsgHash || binary.BigEndian.Uint64(row.Key[20:28]) != stringHash(state.EventKey) {
			return id, errors.New("v2 event identity does not match its key")
		}
	}
	return id, nil
}

func stringHash(value string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return h.Sum64()
}
func eventChannelHash(id string, typ uint8) uint64 {
	return stringHash(id + ":" + strconv.Itoa(int(typ)))
}
