package message_test

import (
	"reflect"
	"testing"

	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestImportedMessageRecordPreservesFullPersistedContent(t *testing.T) {
	want := channelcompat.Message{
		MessageID: 9_007_199_254_740_999, MessageSeq: 257,
		Framer:  frame.Framer{RedDot: true, SyncOnce: true, DUP: true},
		Setting: 7, MsgKey: "key", Expire: 3600, ClientSeq: 15,
		ClientMsgNo: "client-编号", StreamNo: "stream-1", StreamID: 17, StreamFlag: 2,
		Timestamp: 1_700_000_001, ServerTimestampMS: 1_700_000_001_000,
		ChannelID: "room:中文", ChannelType: 2, Topic: "topic", FromUID: "alice",
		Payload: []byte{0, 255, 1, 128},
	}
	record, err := msgdb.EncodeMessageRecord(want, 3)
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != want.MessageID || record.Index != 257 || record.Epoch != 3 || record.SizeBytes != len(record.Payload) {
		t.Fatalf("incorrect record envelope: %+v", record)
	}
	got, err := msgdb.DecodeMessageRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded message differs: got %+v, want %+v", got, want)
	}
	got.Payload[0] = 42
	again, err := msgdb.DecodeMessageRecord(record)
	if err != nil || again.Payload[0] != 0 {
		t.Fatalf("decoded payload aliases the durable record: %v", err)
	}
}
