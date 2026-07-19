package service

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/options"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/require"
)

func TestSystemDeviceBypassesTransientPersonAllowlistState(t *testing.T) {
	previousOptions := options.G
	options.G = options.New()
	t.Cleanup(func() { options.G = previousOptions })

	permission := NewPermissionService(nil)
	reasonCode, err := permission.HasPermissionForSender(
		options.GetFakeChannelIDWith("user-a", "user-b"),
		wkproto.ChannelTypePerson,
		SenderInfo{UID: "user-a", DeviceID: options.G.SystemDeviceId},
	)

	require.NoError(t, err)
	require.Equal(t, wkproto.ReasonSuccess, reasonCode)
}
