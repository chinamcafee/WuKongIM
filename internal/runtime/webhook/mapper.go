package webhook

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strconv"
)

type messageResp struct {
	Header       messageHeader `json:"header"`
	Setting      uint8         `json:"setting"`
	Topic        string        `json:"topic,omitempty"`
	Expire       uint32        `json:"expire"`
	MessageID    uint64        `json:"message_id"`
	MessageIDStr string        `json:"message_idstr"`
	ClientMsgNo  string        `json:"client_msg_no"`
	MessageSeq   uint64        `json:"message_seq"`
	FromUID      string        `json:"from_uid"`
	ChannelID    string        `json:"channel_id"`
	ChannelType  uint8         `json:"channel_type"`
	// ServerTimestampMS is copied from the durable message record without
	// reducing its precision. Downstream consumers use this as the only trusted
	// occurrence time for push-policy decisions.
	ServerTimestampMS int64  `json:"server_timestamp_ms"`
	Payload           []byte `json:"payload"`
}

type messageHeader struct {
	NoPersist uint8 `json:"no_persist"`
	RedDot    uint8 `json:"red_dot"`
	SyncOnce  uint8 `json:"sync_once"`
}

type offlineResp struct {
	MessageResp
	SchemaVersion          int             `json:"schema_version"`
	OfflineTargets         []OfflineTarget `json:"offline_targets,omitempty"`
	Compress               string          `json:"compress,omitempty"`
	CompressOfflineTargets string          `json:"compress_offline_targets,omitempty"`
	SourceID               int64           `json:"source_id,omitempty"`
}

// MessageResp exposes the legacy-compatible encoded message shape for app adapters.
type MessageResp = messageResp

func buildNotifyBody(messages []Message) ([]byte, error) {
	out := make([]messageResp, 0, len(messages))
	for _, msg := range messages {
		out = append(out, messageRespFromMessage(msg))
	}
	return json.Marshal(out)
}

func buildOfflineBody(message OfflineMessage, compressThreshold int) ([]byte, error) {
	targets := canonicalOfflineTargets(message.Targets)
	resp := offlineResp{MessageResp: messageRespFromMessage(message.Message), SchemaVersion: 2}
	if message.Message.SourceID != 0 {
		resp.SourceID = int64(message.Message.SourceID)
	}
	if compressThreshold > 0 && len(targets) >= compressThreshold {
		compressed, err := gzipJSON(targets)
		if err != nil {
			return nil, err
		}
		resp.Compress = "gzip"
		resp.CompressOfflineTargets = base64.StdEncoding.EncodeToString(compressed)
	} else {
		resp.OfflineTargets = targets
	}
	return json.Marshal(resp)
}

func buildOnlineStatusBody(statuses []OnlineStatus) ([]byte, error) {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Value != "" {
			values = append(values, status.Value)
		}
	}
	return json.Marshal(values)
}

func messageRespFromMessage(msg Message) messageResp {
	return messageResp{
		Header: messageHeader{
			NoPersist: 0,
			RedDot:    boolToUint8(msg.RedDot),
			SyncOnce:  boolToUint8(msg.SyncOnce),
		},
		Setting:           msg.Setting,
		Topic:             msg.Topic,
		Expire:            msg.Expire,
		MessageID:         msg.MessageID,
		MessageIDStr:      uint64String(msg.MessageID),
		ClientMsgNo:       msg.ClientMsgNo,
		MessageSeq:        msg.MessageSeq,
		FromUID:           msg.FromUID,
		ChannelID:         msg.ChannelID,
		ChannelType:       msg.ChannelType,
		ServerTimestampMS: msg.ServerTimestampMS,
		Payload:           append([]byte(nil), msg.Payload...),
	}
}

func gzipJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(value); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func canonicalOfflineTargets(values []OfflineTarget) []OfflineTarget {
	targets := append([]OfflineTarget(nil), values...)
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].UID != targets[j].UID {
			return targets[i].UID < targets[j].UID
		}
		return targets[i].DeviceFlag < targets[j].DeviceFlag
	})
	result := targets[:0]
	for _, target := range targets {
		if target.UID == "" {
			continue
		}
		if len(result) != 0 && result[len(result)-1] == target {
			continue
		}
		result = append(result, target)
	}
	return result
}

func boolToUint8(v bool) uint8 {
	if v {
		return 1
	}
	return 0
}

func uint64String(v uint64) string {
	return strconv.FormatUint(v, 10)
}
