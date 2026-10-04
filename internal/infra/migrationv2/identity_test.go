package migrationv2_test

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/stretchr/testify/require"
)

func TestOriginalSourceIdentitiesJoinMembersAndEventCursorsToTheirChannel(t *testing.T) {
	dir := unpackNamedFixture(t, "original-v2-server.tar.gz")
	var channel, member, event, cursor migrationv2.RecordIdentity
	_, err := migrationv2.ReadStoppedNode(context.Background(), migrationv2.NodeOptions{NodeID: 1, Options: migrationv2.Options{DataDir: dir, ShardCount: 2}}, func(row migrationv2.Row) error {
		id, err := migrationv2.Identify(row)
		if err != nil {
			return err
		}
		if row.Kind == migrationv2.Primary {
			switch row.Table {
			case "ChannelInfo":
				if id.Channel.ID == "migrationgroup" {
					channel = id
				}
			case "Subscriber":
				if id.UID == "migrationbob" {
					member = id
				}
			case "MessageEventState":
				event = id
			}
		}
		if row.Table == "MessageEventSeq" {
			cursor = id
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "migrationgroup", channel.Channel.ID)
	require.Equal(t, uint8(2), channel.Channel.Type)
	require.NotZero(t, channel.ChannelHash)
	require.Equal(t, channel.ChannelHash, member.ChannelHash)
	require.Equal(t, channel.Channel, event.Channel)
	require.Equal(t, "original-v2-0", event.ClientMsgNo)
	require.Equal(t, event.EventChannelHash, cursor.EventChannelHash)
	require.Equal(t, event.ClientMsgHash, cursor.ClientMsgHash)
}
