package pluginevents

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPersistAfterCommittedCloneCopiesMutableFields(t *testing.T) {
	event := PersistAfterCommitted{
		MessageID:         10,
		MessageSeq:        3,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "u1",
		ClientMsgNo:       "c1",
		ServerTimestampMS: 1713859200123,
		Payload:           []byte("hello"),
		MessageScopedUIDs: []string{"u2"},
	}
	clone := event.Clone()
	event.Payload[0] = 'H'
	event.MessageScopedUIDs[0] = "changed"

	require.Equal(t, []byte("hello"), clone.Payload)
	require.Equal(t, []string{"u2"}, clone.MessageScopedUIDs)
}

func TestReceiveOfflineCloneCopiesMutableFields(t *testing.T) {
	event := ReceiveOffline{
		MessageID:         10,
		MessageSeq:        3,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "u1",
		UID:               "u2",
		ClientMsgNo:       "c1",
		ServerTimestampMS: 1713859200123,
		Payload:           []byte("hello"),
		MessageScopedUIDs: []string{"u2"},
	}
	clone := event.Clone()
	event.Payload[0] = 'H'
	event.MessageScopedUIDs[0] = "changed"

	require.Equal(t, []byte("hello"), clone.Payload)
	require.Equal(t, []string{"u2"}, clone.MessageScopedUIDs)
}

func TestReceiveOfflineBatchCloneCopiesMutableFields(t *testing.T) {
	event := ReceiveOfflineBatch{
		MessageID:         10,
		MessageSeq:        3,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "u1",
		Targets:           []ReceiveOfflineTarget{{UID: "u2", DeviceFlag: 0}, {UID: "u3", DeviceFlag: 2}},
		ClientMsgNo:       "c1",
		ServerTimestampMS: 1713859200123,
		Payload:           []byte("hello"),
		MessageScopedUIDs: []string{"u2"},
	}
	clone := event.Clone()
	event.Targets[0].UID = "changed"
	event.Payload[0] = 'H'
	event.MessageScopedUIDs[0] = "changed"

	require.Equal(t, []ReceiveOfflineTarget{{UID: "u2", DeviceFlag: 0}, {UID: "u3", DeviceFlag: 2}}, clone.Targets)
	require.Equal(t, []byte("hello"), clone.Payload)
	require.Equal(t, []string{"u2"}, clone.MessageScopedUIDs)
}

func TestReceiveOfflineBatchForTargetPreservesSharedMessageFields(t *testing.T) {
	batch := ReceiveOfflineBatch{
		MessageID:         10,
		MessageSeq:        3,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "u1",
		ClientMsgNo:       "c1",
		ServerTimestampMS: 1713859200123,
		Payload:           []byte("hello"),
		NoPersist:         true,
		SyncOnce:          true,
		MessageScopedUIDs: []string{"u2"},
	}

	require.Equal(t, ReceiveOffline{
		MessageID:         10,
		MessageSeq:        3,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "u1",
		UID:               "u3",
		DeviceFlag:        2,
		ClientMsgNo:       "c1",
		ServerTimestampMS: 1713859200123,
		Payload:           []byte("hello"),
		NoPersist:         true,
		SyncOnce:          true,
		MessageScopedUIDs: []string{"u2"},
	}, batch.ForTarget(ReceiveOfflineTarget{UID: "u3", DeviceFlag: 2}))
}
