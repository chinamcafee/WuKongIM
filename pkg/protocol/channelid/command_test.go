package channelid

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCommandChannel(t *testing.T) {
	require.True(t, IsCommandChannel("g1____cmd"))
	require.False(t, IsCommandChannel("g1"))
}

func TestToCommandChannel(t *testing.T) {
	require.Equal(t, "g1____cmd", ToCommandChannel("g1"))
	require.Equal(t, "g1____cmd", ToCommandChannel("g1____cmd"))
}

func TestFromCommandChannel(t *testing.T) {
	channelID, ok := FromCommandChannel("g1____cmd")
	require.True(t, ok)
	require.Equal(t, "g1", channelID)

	channelID, ok = FromCommandChannel("g1")
	require.False(t, ok)
	require.Equal(t, "g1", channelID)

	channelID, ok = FromCommandChannel("u2@u1____cmd")
	require.True(t, ok)
	require.Equal(t, "u2@u1", channelID)
}

func TestConfiguredCommandCodecsAreIndependent(t *testing.T) {
	for _, suffix := range []string{"__commands", "__events"} {
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			codec := CommandCodec{Suffix: suffix}
			id := codec.ToCommandChannel("u1@u2")
			require.Equal(t, "u1@u2"+suffix, id)
			require.Equal(t, id, codec.ToCommandChannel(id))
			source, ok := codec.FromCommandChannel(id)
			require.True(t, ok)
			require.Equal(t, "u1@u2", source)
			require.False(t, codec.IsCommandChannel("g1____cmd"))
			scoped, err := codec.RequestSubscriberChannelFor([]string{"u1", "u2"})
			require.NoError(t, err)
			require.Equal(t, scoped.SourceChannelID+suffix, scoped.CommandChannelID)
			require.Equal(t, "g1____cmd", ToCommandChannel("g1"))
		})
	}
}
