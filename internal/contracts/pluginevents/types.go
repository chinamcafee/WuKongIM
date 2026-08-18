package pluginevents

// PersistAfterCommitted carries one durable committed message into plugin hooks.
type PersistAfterCommitted struct {
	// MessageID is the durable server message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence number.
	MessageSeq uint64
	// ChannelID is the target channel identifier.
	ChannelID string
	// ChannelType is the target channel type.
	ChannelType uint8
	// FromUID is the sender user identifier.
	FromUID string
	// SenderNodeID is the node identifier that accepted the sender session.
	SenderNodeID uint64
	// SenderSessionID is the gateway session identifier that submitted the message.
	SenderSessionID uint64
	// ClientMsgNo is the client-provided idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server commit timestamp in milliseconds.
	ServerTimestampMS int64
	// Payload is the committed message body.
	Payload []byte
	// RedDot reports whether this message should affect unread counters.
	RedDot bool
	// SyncOnce reports whether the message should only sync once to recipients.
	SyncOnce bool
	// MessageScopedUIDs limits plugin-visible recipient scope for this message.
	MessageScopedUIDs []string
}

// Clone returns an independent event copy safe for asynchronous plugin workers.
func (e PersistAfterCommitted) Clone() PersistAfterCommitted {
	e.Payload = append([]byte(nil), e.Payload...)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}

// ReceiveOffline carries one offline recipient candidate into plugin hooks.
type ReceiveOffline struct {
	// MessageID is the durable server message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence number.
	MessageSeq uint64
	// ChannelID is the target channel identifier.
	ChannelID string
	// ChannelType is the target channel type.
	ChannelType uint8
	// FromUID is the sender user identifier.
	FromUID string
	// UID is the offline recipient user identifier.
	UID string
	// DeviceFlag is the offline recipient protocol device category.
	DeviceFlag uint8
	// ClientMsgNo is the client-provided idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server commit timestamp in milliseconds.
	ServerTimestampMS int64
	// Payload is the committed message body.
	Payload []byte
	// NoPersist reports whether the envelope was a transient realtime send.
	NoPersist bool
	// SyncOnce reports whether the message should only sync once to recipients.
	SyncOnce bool
	// MessageScopedUIDs marks request-scoped messages that must not trigger Receive.
	MessageScopedUIDs []string
}

// Clone returns an independent event copy safe for asynchronous plugin workers.
func (e ReceiveOffline) Clone() ReceiveOffline {
	e.Payload = append([]byte(nil), e.Payload...)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}

// ReceiveOfflineTarget is one recipient/device-shape plugin target.
type ReceiveOfflineTarget struct {
	UID        string
	DeviceFlag uint8
}

// ReceiveOfflineBatch carries offline recipient candidates for one committed message into plugin hooks.
type ReceiveOfflineBatch struct {
	// MessageID is the durable server message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence number.
	MessageSeq uint64
	// ChannelID is the target channel identifier.
	ChannelID string
	// ChannelType is the target channel type.
	ChannelType uint8
	// FromUID is the sender user identifier.
	FromUID string
	// Targets contains canonical offline recipient/device shapes.
	Targets []ReceiveOfflineTarget
	// ClientMsgNo is the client-provided idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server commit timestamp in milliseconds.
	ServerTimestampMS int64
	// Payload is the committed message body shared by all recipients.
	Payload []byte
	// NoPersist reports whether the envelope was a transient realtime send.
	NoPersist bool
	// SyncOnce reports whether the message should only sync once to recipients.
	SyncOnce bool
	// MessageScopedUIDs marks request-scoped messages that must not trigger Receive.
	MessageScopedUIDs []string
}

// Clone returns an independent batch copy safe for asynchronous plugin workers.
func (e ReceiveOfflineBatch) Clone() ReceiveOfflineBatch {
	e.Targets = append([]ReceiveOfflineTarget(nil), e.Targets...)
	e.Payload = append([]byte(nil), e.Payload...)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}

// ForTarget projects one recipient/device shape into the scalar event.
// Payload and MessageScopedUIDs remain shared with the immutable owned batch.
func (e ReceiveOfflineBatch) ForTarget(target ReceiveOfflineTarget) ReceiveOffline {
	return ReceiveOffline{
		MessageID:         e.MessageID,
		MessageSeq:        e.MessageSeq,
		ChannelID:         e.ChannelID,
		ChannelType:       e.ChannelType,
		FromUID:           e.FromUID,
		UID:               target.UID,
		DeviceFlag:        target.DeviceFlag,
		ClientMsgNo:       e.ClientMsgNo,
		ServerTimestampMS: e.ServerTimestampMS,
		Payload:           e.Payload,
		NoPersist:         e.NoPersist,
		SyncOnce:          e.SyncOnce,
		MessageScopedUIDs: e.MessageScopedUIDs,
	}
}
