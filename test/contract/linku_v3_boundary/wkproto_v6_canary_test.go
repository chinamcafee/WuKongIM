package linku_v3_boundary_test

import (
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestLinkUWKProtoV6Canary(t *testing.T) {
	if frame.LatestVersion != 6 {
		t.Fatalf("LatestVersion = %d, want WKProto v6", frame.LatestVersion)
	}
	if frame.ReasonNotInWhitelist != 13 || frame.ReasonSendBan != 25 || frame.ReasonProtocolUpgradeRequired != 27 {
		t.Fatalf("Link-U reason codes changed: whitelist=%d send_ban=%d upgrade=%d",
			frame.ReasonNotInWhitelist, frame.ReasonSendBan, frame.ReasonProtocolUpgradeRequired)
	}

	maxSeq := uint64(math.MaxUint64)
	frames := []frame.Frame{
		&frame.ConnectPacket{
			Version: frame.LatestVersion, DeviceFlag: frame.APP, DeviceID: "device-v6",
			UID: "user-v6", Token: "token-v6", ClientTimestamp: 123, ClientKey: "client-key",
		},
		&frame.ConnackPacket{
			Framer: frame.Framer{HasServerVersion: true}, ServerVersion: frame.LatestVersion,
			ReasonCode: frame.ReasonSuccess, NodeId: 3, ServerKey: "server-key", Salt: "salt",
		},
		&frame.SendPacket{
			ClientSeq: 9, ClientMsgNo: "send-v6", ChannelID: "recipient-v6",
			ChannelType: frame.ChannelTypePerson, Payload: []byte("payload-v6"),
		},
		&frame.SendackPacket{
			MessageID: 7, MessageSeq: maxSeq, ClientSeq: 9,
			ClientMsgNo: "send-v6", ReasonCode: frame.ReasonNotInWhitelist,
		},
		&frame.RecvPacket{
			MessageID: 8, MessageSeq: maxSeq, ClientMsgNo: "recv-v6", Timestamp: 123,
			ChannelID: "sender-v6", ChannelType: frame.ChannelTypePerson,
			FromUID: "sender-v6", Payload: []byte("payload-v6"),
		},
		&frame.RecvackPacket{MessageID: 8, MessageSeq: maxSeq},
	}

	proto := codec.New()
	for _, original := range frames {
		encoded, err := proto.EncodeFrame(original, frame.LatestVersion)
		if err != nil {
			t.Fatalf("encode %T: %v", original, err)
		}
		decoded, consumed, err := proto.DecodeFrame(encoded, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode %T: %v", original, err)
		}
		if consumed != len(encoded) || decoded.GetFrameType() != original.GetFrameType() {
			t.Fatalf("round trip %T consumed=%d/%d type=%v", original, consumed, len(encoded), decoded.GetFrameType())
		}
		assertV6SequenceAndCorrelation(t, original, decoded, maxSeq)
	}
}

func assertV6SequenceAndCorrelation(t *testing.T, original, decoded frame.Frame, maxSeq uint64) {
	t.Helper()
	switch got := decoded.(type) {
	case *frame.ConnectPacket:
		want := original.(*frame.ConnectPacket)
		if got.Version != frame.LatestVersion || got.UID != want.UID || got.Token != want.Token {
			t.Fatalf("CONNECT round trip = %#v", got)
		}
	case *frame.ConnackPacket:
		if got.ServerVersion != frame.LatestVersion || got.ReasonCode != frame.ReasonSuccess {
			t.Fatalf("CONNACK round trip = %#v", got)
		}
	case *frame.SendPacket:
		want := original.(*frame.SendPacket)
		if got.ClientSeq != want.ClientSeq || got.ClientMsgNo != want.ClientMsgNo || string(got.Payload) != string(want.Payload) {
			t.Fatalf("SEND round trip = %#v", got)
		}
	case *frame.SendackPacket:
		if got.MessageSeq != maxSeq || got.ClientMsgNo != "send-v6" || got.ReasonCode != frame.ReasonNotInWhitelist {
			t.Fatalf("SENDACK round trip = %#v", got)
		}
	case *frame.RecvPacket:
		if got.MessageSeq != maxSeq || got.ClientMsgNo != "recv-v6" || string(got.Payload) != "payload-v6" {
			t.Fatalf("RECV round trip = %#v", got)
		}
	case *frame.RecvackPacket:
		if got.MessageSeq != maxSeq || got.MessageID != 8 {
			t.Fatalf("RECVACK round trip = %#v", got)
		}
	default:
		t.Fatalf("unexpected decoded frame %T", decoded)
	}
}
