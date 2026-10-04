package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

type transformFixtureDecoder struct{}

func (transformFixtureDecoder) DecodeBusiness(r Row, _ RecordIdentity) (f BusinessFacts, err error) {
	err = UnmarshalState(r.Value, &f)
	return
}
func transformFixture(t *testing.T, facts []BusinessFacts) (Workspace, SourceSelection) {
	w := dedupeTestWorkspace(t)
	for i, f := range facts {
		data, err := MarshalState(f)
		require.NoError(t, err)
		ch := ChannelIdentity{ID: "group", Type: 2}
		table := "Message"
		kind := Primary
		var seq uint64
		if f.Message != nil {
			seq = f.Message.MessageSeq
			ch.ID = f.Message.ChannelID
		}
		if f.Tail != nil {
			ch = f.Tail.Channel
			kind = Other
		}
		if f.Conversation != nil {
			ch = f.Conversation.Channel
			table = "Conversation"
		}
		client := ""
		if f.EventState != nil {
			table = "MessageEventState"
			ch = ChannelIdentity{ID: f.EventState.ChannelID, Type: uint8(f.EventState.ChannelType)}
			client = f.EventState.ClientMsgNo
		}
		if f.EventCursor != nil {
			table = "MessageEventSeq"
			kind = Other
			ch = ChannelIdentity{ID: f.EventCursor.ChannelID, Type: uint8(f.EventCursor.ChannelType)}
			client = f.EventCursor.ClientMsgNo
		}
		owner := uint64(1)
		if ch.ID != "group" {
			owner = 2
		}
		r := Row{Table: table, Kind: kind, ID: seq, Owner: owner, Key: []byte(fmt.Sprintf("row-%d", i)), Value: data}
		ref := sourceCandidate{NodeID: 1, Table: table, Kind: kind, SourceKey: sourceRowKey(1, r), Identity: RecordIdentity{Channel: ch, ClientMsgNo: client}, LogicalKey: fmt.Sprint(i)}
		encoded, err := MarshalState(ref)
		require.NoError(t, err)
		raw, err := json.Marshal(r)
		require.NoError(t, err)
		require.NoError(t, w.Put(context.Background(), []transfer.SpoolRow{{Key: ref.SourceKey, Value: raw}, {Key: selectedKey(table, ref.LogicalKey), Value: encoded}}))
	}
	return w, SourceSelection{Digest: "source", Messages: &MessagePolicy{KeepLatestDuplicates: true, ExcludeCMD: true, CompactSequences: true}}
}
func transformMessage(seq, id uint64, key string, cmd bool) BusinessFacts {
	return BusinessFacts{CMDMessage: cmd, Message: &channelcompat.Message{ChannelID: "group", ChannelType: 2, MessageID: id, MessageSeq: seq, FromUID: "sender", ClientMsgNo: key, Payload: []byte(fmt.Sprint(seq))}}
}
func TestMessageTransformExcludesCMDBeforeDedupeAndPreservesReadSets(t *testing.T) {
	facts := []BusinessFacts{transformMessage(1, 10, "a", false), transformMessage(2, 20, "b", true), transformMessage(3, 10, "a", false), transformMessage(4, 30, "c", false), transformMessage(5, 30, "c", true), {Tail: &SourceMessageTail{Channel: ChannelIdentity{ID: "group", Type: 2}, LastSeq: 5}}}
	w, s := transformFixture(t, facts)
	tr, err := buildMessageTransform(context.Background(), s, w, transformFixtureDecoder{}, "test-transform/")
	require.NoError(t, err)
	require.EqualValues(t, 2, tr.report.CMDDrops)
	require.EqualValues(t, 1, tr.report.DuplicateDrops)
	require.EqualValues(t, 2, tr.report.Retained)
	for old, want := range map[uint64]uint64{0: 0, 1: 0, 2: 0, 3: 1, 4: 2, 5: 2, 100: 2} {
		got, err := tr.boundary(context.Background(), ChannelIdentity{ID: "group", Type: 2}, old)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	// Verification rebuilds from source in a separate namespace with the same digest.
	independent, err := buildMessageTransform(context.Background(), s, w, transformFixtureDecoder{}, "independent/")
	require.NoError(t, err)
	require.Equal(t, tr.report, independent.report)
	mapped, omit, err := tr.apply(context.Background(), RecordIdentity{Channel: ChannelIdentity{ID: "group", Type: 2}}, transformMessage(4, 30, "c", false))
	require.NoError(t, err)
	require.False(t, omit)
	require.EqualValues(t, 2, mapped.Message.MessageSeq)
}
func TestMessageTransformRejectsGapsTailMismatchAndSupersededWinners(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts []BusinessFacts
		tail  uint64
		want  string
	}{
		{"gap", []BusinessFacts{transformMessage(2, 10, "a", false)}, 2, "pre-existing source sequence gap"},
		{"tail", []BusinessFacts{transformMessage(1, 10, "a", false)}, 2, "differs from durable tail"},
		{"chain", []BusinessFacts{transformMessage(1, 10, "a", false), transformMessage(2, 10, "b", false), transformMessage(3, 20, "b", false)}, 3, "winner is superseded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.facts = append(tc.facts, BusinessFacts{Tail: &SourceMessageTail{Channel: ChannelIdentity{ID: "group", Type: 2}, LastSeq: tc.tail}})
			w, s := transformFixture(t, tc.facts)
			_, err := buildMessageTransform(context.Background(), s, w, transformFixtureDecoder{}, "test-transform/")
			require.ErrorContains(t, err, tc.want)
		})
	}
}
