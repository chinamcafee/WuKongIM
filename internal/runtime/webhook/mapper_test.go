package webhook

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestBuildNotifyBodyMapsCommittedMessages(t *testing.T) {
	body, err := buildNotifyBody([]Message{{
		MessageID:         42,
		MessageSeq:        7,
		ChannelID:         "group-a",
		ChannelType:       2,
		Setting:           9,
		Topic:             "topic-a",
		Expire:            3600,
		FromUID:           "alice",
		ClientMsgNo:       "client-1",
		ServerTimestampMS: time.Unix(10, 0).UnixMilli(),
		Payload:           []byte("hello"),
		RedDot:            true,
	}})
	if err != nil {
		t.Fatalf("buildNotifyBody() error = %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	msg := got[0]
	if msg["message_id"] != float64(42) || msg["message_seq"] != float64(7) {
		t.Fatalf("message id/seq = %#v", msg)
	}
	if msg["setting"] != float64(9) {
		t.Fatalf("setting = %v, want 9", msg["setting"])
	}
	if msg["topic"] != "topic-a" || msg["expire"] != float64(3600) {
		t.Fatalf("topic/expire = %v/%v, want topic-a/3600", msg["topic"], msg["expire"])
	}
	if msg["channel_id"] != "group-a" || msg["from_uid"] != "alice" {
		t.Fatalf("channel/from = %#v", msg)
	}
	if msg["server_timestamp_ms"] != float64(10000) {
		t.Fatalf("server_timestamp_ms = %v, want 10000", msg["server_timestamp_ms"])
	}
	if _, exists := msg["timestamp"]; exists {
		t.Fatalf("legacy second-resolution timestamp must not be emitted: %#v", msg)
	}
	if msg["payload"] != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Fatalf("payload = %v", msg["payload"])
	}
	header, ok := msg["header"].(map[string]any)
	if !ok {
		t.Fatalf("header = %#v, want object", msg["header"])
	}
	if header["red_dot"] != float64(1) || header["sync_once"] != float64(0) || header["no_persist"] != float64(0) {
		t.Fatalf("header = %#v", header)
	}
}

func TestBuildOfflineBodyCompressesCanonicalDeviceTargets(t *testing.T) {
	body, err := buildOfflineBody(OfflineMessage{
		Message: Message{
			MessageID:         10,
			MessageSeq:        11,
			ChannelID:         "group-a",
			ChannelType:       2,
			Setting:           9,
			Topic:             "topic-a",
			Expire:            3600,
			SourceID:          7,
			FromUID:           "alice",
			ServerTimestampMS: time.Unix(10, 0).UnixMilli(),
			Payload:           []byte("payload"),
		},
		Targets: []OfflineTarget{
			{UID: "u3", DeviceFlag: 2},
			{UID: "u1", DeviceFlag: 0},
			{UID: "u2", DeviceFlag: 0},
			{UID: "u1", DeviceFlag: 0},
		},
	}, 2)
	if err != nil {
		t.Fatalf("buildOfflineBody() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if got["compress"] != "gzip" {
		t.Fatalf("compress = %v, want gzip", got["compress"])
	}
	if got["schema_version"] != float64(2) {
		t.Fatalf("schema_version = %v, want 2", got["schema_version"])
	}
	if got["compress_offline_targets"] == "" {
		t.Fatalf("compress_offline_targets is empty")
	}
	if got["setting"] != float64(9) || got["topic"] != "topic-a" || got["expire"] != float64(3600) || got["source_id"] != float64(7) {
		t.Fatalf("legacy offline fields = setting:%v topic:%v expire:%v source:%v, want 9/topic-a/3600/7", got["setting"], got["topic"], got["expire"], got["source_id"])
	}
	if got["server_timestamp_ms"] != float64(10000) {
		t.Fatalf("server_timestamp_ms = %v, want 10000", got["server_timestamp_ms"])
	}
	if _, exists := got["timestamp"]; exists {
		t.Fatalf("legacy second-resolution timestamp must not be emitted: %#v", got)
	}
	compressed, ok := got["compress_offline_targets"].(string)
	if !ok {
		t.Fatalf("compress_offline_targets = %#v, want string", got["compress_offline_targets"])
	}
	targets := decodeCompressedOfflineTargets(t, compressed)
	if want := []OfflineTarget{{UID: "u1", DeviceFlag: 0}, {UID: "u2", DeviceFlag: 0}, {UID: "u3", DeviceFlag: 2}}; !reflect.DeepEqual(targets, want) {
		t.Fatalf("compressed targets = %#v, want %#v", targets, want)
	}
	if _, exists := got["offline_targets"]; exists {
		t.Fatalf("offline_targets exists for compressed body: %#v", got)
	}
	if _, exists := got["to_uids"]; exists {
		t.Fatalf("legacy to_uids exists in v2 body: %#v", got)
	}
}

func TestBuildOfflineBodyIncludesDeviceTargetsBelowCompressThreshold(t *testing.T) {
	body, err := buildOfflineBody(OfflineMessage{
		Message: Message{
			MessageID:         10,
			MessageSeq:        11,
			ChannelID:         "group-a",
			ChannelType:       2,
			FromUID:           "alice",
			ServerTimestampMS: time.Unix(10, 0).UnixMilli(),
			Payload:           []byte("payload"),
		},
		Targets: []OfflineTarget{{UID: "u1", DeviceFlag: 0}, {UID: "u2", DeviceFlag: 2}},
	}, 3)
	if err != nil {
		t.Fatalf("buildOfflineBody() error = %v", err)
	}
	var got struct {
		SchemaVersion          int             `json:"schema_version"`
		OfflineTargets         []OfflineTarget `json:"offline_targets"`
		Compress               string          `json:"compress"`
		CompressOfflineTargets string          `json:"compress_offline_targets"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if got.SchemaVersion != 2 {
		t.Fatalf("schema_version = %d, want 2", got.SchemaVersion)
	}
	if want := []OfflineTarget{{UID: "u1", DeviceFlag: 0}, {UID: "u2", DeviceFlag: 2}}; !reflect.DeepEqual(got.OfflineTargets, want) {
		t.Fatalf("offline_targets = %#v, want %#v", got.OfflineTargets, want)
	}
	if got.Compress != "" || got.CompressOfflineTargets != "" {
		t.Fatalf("compressed fields = %q/%q, want empty", got.Compress, got.CompressOfflineTargets)
	}
}

func TestBuildOnlineStatusBodyFiltersEmptyValues(t *testing.T) {
	body, err := buildOnlineStatusBody([]OnlineStatus{
		{Value: "u1-1"},
		{},
		{Value: "u2-0"},
	})
	if err != nil {
		t.Fatalf("buildOnlineStatusBody() error = %v", err)
	}
	var got []string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if want := []string{"u1-1", "u2-0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %#v, want %#v", got, want)
	}
}

func decodeCompressedOfflineTargets(t *testing.T, encoded string) []OfflineTarget {
	t.Helper()

	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer zr.Close()
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	var got []OfflineTarget
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(data))
	}
	return got
}
