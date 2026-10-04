package migration

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func streamTransformMessage(seq, id uint64, client string, legacy bool) BusinessFacts {
	f := transformMessage(seq, id, client, false)
	if legacy {
		f.Message.StreamNo = "legacy-stream"
	} else {
		f.Message.Setting = 2
	}
	return f
}

func TestStreamClassificationUsesProtocolFields(t *testing.T) {
	for _, tc := range []struct {
		message channelcompat.Message
		want    bool
	}{
		{channelcompat.Message{Setting: 2}, true},
		{channelcompat.Message{Setting: 3}, true},
		{channelcompat.Message{StreamNo: "old"}, true},
		{channelcompat.Message{Setting: 1, Payload: []byte("stream")}, false},
		{channelcompat.Message{}, false},
	} {
		require.Equal(t, tc.want, IsStreamMessage(tc.message))
	}
	require.NoError(t, validateMessagePolicy(&MessagePolicy{ExcludeStreams: true, CompactSequences: true}))
	require.Error(t, validateMessagePolicy(&MessagePolicy{ExcludeStreams: true}))
}

func TestStreamExclusionCompactsAllPositionsAndPreservesReadBoundaries(t *testing.T) {
	ctx := context.Background()
	ch := ChannelIdentity{ID: "group", Type: 2}
	// The newer excluded stream shares both keys with ordinary seq 2. It must
	// never win dedupe, and stream-only channels must have no imported log tail.
	facts := []BusinessFacts{
		streamTransformMessage(1, 10, "leading", false),
		transformMessage(2, 20, "ordinary", false),
		streamTransformMessage(3, 20, "ordinary", true),
		transformMessage(4, 40, "last-ordinary", false),
		streamTransformMessage(5, 50, "trailing", false),
		{Tail: &SourceMessageTail{Channel: ch, LastSeq: 5}},
	}
	only := streamTransformMessage(1, 60, "only", false)
	only.Message.ChannelID = "only-stream"
	empty := ChannelIdentity{ID: "only-stream", Type: 2}
	facts = append(facts, only, BusinessFacts{Tail: &SourceMessageTail{Channel: empty, LastSeq: 1}})
	w, s := transformFixture(t, facts)
	s.Messages.ExcludeStreams = true
	tr, err := buildMessageTransform(ctx, s, w, transformFixtureDecoder{}, "convert/")
	require.NoError(t, err)
	require.EqualValues(t, 4, tr.report.StreamDrops)
	require.Zero(t, tr.report.DuplicateDrops)
	require.EqualValues(t, 2, tr.report.Retained)
	for old, want := range map[uint64]uint64{0: 0, 1: 0, 2: 1, 3: 1, 4: 2, 5: 2, 99: 2} {
		got, omitted, err := tr.apply(ctx, RecordIdentity{Channel: ch}, BusinessFacts{Conversation: &SourceConversation{Channel: ch, ReadSeq: old, DeletedToSeq: old}})
		require.NoError(t, err)
		require.False(t, omitted)
		require.Equal(t, want, got.Conversation.ReadSeq)
		require.Equal(t, want, got.Conversation.DeletedToSeq)
	}
	for i, original := range []uint64{2, 4} {
		got, omitted, err := tr.apply(ctx, RecordIdentity{Channel: ch}, facts[original-1])
		require.NoError(t, err)
		require.False(t, omitted)
		require.EqualValues(t, i+1, got.Message.MessageSeq)
	}
	_, omitted, err := tr.apply(ctx, RecordIdentity{Channel: empty}, BusinessFacts{Tail: &SourceMessageTail{Channel: empty, LastSeq: 1}})
	require.NoError(t, err)
	require.True(t, omitted)
	boundary, err := tr.boundary(ctx, empty, 1)
	require.NoError(t, err)
	require.Zero(t, boundary)
	// The immutable workspace rejects changed conversion mappings. Verification
	// still reconstructs expectations in a separate namespace from original rows.
	key := "mapping/" + channelTuple(ch) + "/00000000000000000002"
	require.ErrorContains(t, tr.w.Put(ctx, []transfer.SpoolRow{{Key: []byte(key), Value: []byte(`{}`)}}), "durable key conflict")
	independent, err := buildMessageTransform(ctx, s, w, transformFixtureDecoder{}, "verify/")
	require.NoError(t, err)
	require.Equal(t, tr.report, independent.report)
	got, _, err := independent.apply(ctx, RecordIdentity{Channel: ch}, transformMessage(2, 20, "ordinary", false))
	require.NoError(t, err)
	require.EqualValues(t, 1, got.Message.MessageSeq)
}

func TestStreamExclusionRejectsOriginalGapsEvenWhenEverythingIsOmitted(t *testing.T) {
	facts := []BusinessFacts{streamTransformMessage(2, 20, "gap", false), {Tail: &SourceMessageTail{Channel: ChannelIdentity{ID: "group", Type: 2}, LastSeq: 2}}}
	w, s := transformFixture(t, facts)
	s.Messages.ExcludeStreams = true
	_, err := buildMessageTransform(context.Background(), s, w, transformFixtureDecoder{}, "stream/")
	require.ErrorContains(t, err, "pre-existing source sequence gap")
}

func TestStreamExclusionOmitsOnlyUnambiguousAssociatedEvents(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "separate_keys", true: "shared_key"}[shared], func(t *testing.T) {
			ctx := context.Background()
			ch := ChannelIdentity{ID: "group", Type: 2}
			client := "ordinary"
			if shared {
				client = "stream"
			}
			facts := []BusinessFacts{streamTransformMessage(1, 10, "stream", false), transformMessage(2, 20, client, false), {Tail: &SourceMessageTail{Channel: ch, LastSeq: 2}},
				{EventState: &meta.MessageEventState{ChannelID: ch.ID, ChannelType: 2, ClientMsgNo: "stream"}},
				{EventCursor: &meta.MessageEventCursor{ChannelID: ch.ID, ChannelType: 2, ClientMsgNo: "stream"}},
				{EventState: &meta.MessageEventState{ChannelID: ch.ID, ChannelType: 2, ClientMsgNo: "unrelated"}},
			}
			w, s := transformFixture(t, facts)
			s.Messages.ExcludeStreams = true
			tr, err := buildMessageTransform(ctx, s, w, transformFixtureDecoder{}, "stream/")
			if shared {
				require.ErrorContains(t, err, "shared with a retained message")
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 1, tr.report.StreamEventStates)
			require.EqualValues(t, 1, tr.report.StreamEventCursors)
			for _, client := range []string{"stream", "unrelated"} {
				_, omit, err := tr.apply(ctx, RecordIdentity{Channel: ch, ClientMsgNo: client}, BusinessFacts{EventState: &meta.MessageEventState{ClientMsgNo: client}})
				require.NoError(t, err)
				require.Equal(t, client == "stream", omit)
			}
		})
	}
}
