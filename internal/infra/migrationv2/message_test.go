package migrationv2_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestOriginalV2MessageConvertsWithoutChangingDurableContent(t *testing.T) {
	var got channelcompat.Message
	var term uint64
	require.NoError(t, migrationv2.Scan(context.Background(), migrationv2.Options{DataDir: unpackFixture(t), ShardCount: 2}, func(row migrationv2.Row) (err error) {
		if row.Table == "Message" && row.Kind == migrationv2.Primary {
			got, term, err = migrationv2.DecodeMessage(row)
		}
		return err
	}))
	want := channelcompat.Message{
		MessageID: 9007199254740999, MessageSeq: 257, Framer: frame.Framer{RedDot: true, SyncOnce: true, DUP: true},
		Expire: 3600, ClientMsgNo: "client-一", StreamNo: "stream-一", Timestamp: 1700000001,
		ChannelID: "群:一-2", ChannelType: 2, Topic: "topic-一", FromUID: "用户:一", Payload: []byte{0, 1, 255, 0},
		ServerTimestampMS: 1700000001000,
	}
	require.Equal(t, want, got)
	require.Equal(t, uint64(3), term)
	record, err := messagedb.EncodeMessageRecord(got, 1)
	require.NoError(t, err)
	decoded, err := messagedb.DecodeMessageRecord(record)
	require.NoError(t, err)
	require.Equal(t, want, decoded)
}
