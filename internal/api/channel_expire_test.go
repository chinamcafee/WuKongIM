package api

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/options"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/require"
)

func TestActualChannelIDForExpireNormalizesPersonPair(t *testing.T) {
	forward := actualChannelIDForExpire(channelExpireUpdateReq{
		ChannelId:   "user-a",
		ToUid:       "user-b",
		ChannelType: wkproto.ChannelTypePerson,
	})
	reverse := actualChannelIDForExpire(channelExpireUpdateReq{
		ChannelId:   "user-b",
		ToUid:       "user-a",
		ChannelType: wkproto.ChannelTypePerson,
	})

	require.Equal(t, options.GetFakeChannelIDWith("user-a", "user-b"), forward)
	require.Equal(t, forward, reverse)
}

func TestActualChannelIDForExpireLeavesExplicitChannelUnchanged(t *testing.T) {
	require.Equal(t, "group-1", actualChannelIDForExpire(channelExpireUpdateReq{
		ChannelId:   "group-1",
		ToUid:       "ignored-user",
		ChannelType: wkproto.ChannelTypeGroup,
	}))
	require.Equal(t, "user-a@user-b", actualChannelIDForExpire(channelExpireUpdateReq{
		ChannelId:   "user-a@user-b",
		ChannelType: wkproto.ChannelTypePerson,
	}))
}
