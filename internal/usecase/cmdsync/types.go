package cmdsync

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrUIDRequired reports a missing user id in CMD sync commands.
	ErrUIDRequired = errors.New("internal/usecase/cmdsync: uid required")
	// ErrChannelRequired reports a missing source channel id in CMD binding commands.
	ErrChannelRequired = errors.New("internal/usecase/cmdsync: channel id required")
	// ErrChannelTypeRequired reports a missing source channel type in CMD binding commands.
	ErrChannelTypeRequired = errors.New("internal/usecase/cmdsync: channel type required")
	// ErrSequenceExhausted reports a command channel whose tail cannot produce a new start sequence.
	ErrSequenceExhausted = errors.New("internal/usecase/cmdsync: command channel sequence exhausted")
	// ErrStateStoreRequired reports a missing durable CMD state dependency.
	ErrStateStoreRequired = errors.New("internal/usecase/cmdsync: state store required")
	// ErrMessageStoreRequired reports a missing command-channel message dependency.
	ErrMessageStoreRequired = errors.New("internal/usecase/cmdsync: message store required")
	// ErrDeviceStateStoreRequired reports a missing device-scoped cursor dependency.
	ErrDeviceStateStoreRequired = errors.New("internal/usecase/cmdsync: device state store required")
	// ErrPrincipalStoreRequired reports a missing authoritative credential dependency.
	ErrPrincipalStoreRequired = errors.New("internal/usecase/cmdsync: principal store required")
	// ErrPrincipalInvalid reports a structurally invalid trusted principal.
	ErrPrincipalInvalid = errors.New("internal/usecase/cmdsync: invalid command principal")
	// ErrPrincipalStale reports a principal that no longer matches durable device authority.
	ErrPrincipalStale = errors.New("internal/usecase/cmdsync: stale command principal")
	// ErrBatchIDRequired reports a missing v3 command batch identifier.
	ErrBatchIDRequired = errors.New("internal/usecase/cmdsync: batch id required")
	// ErrBatchIDMismatch reports that an ACK does not match its explicit channel cursors.
	ErrBatchIDMismatch = errors.New("internal/usecase/cmdsync: batch id mismatch")
	// ErrAckCursorInvalid reports an invalid or duplicate v3 command ACK cursor.
	ErrAckCursorInvalid = errors.New("internal/usecase/cmdsync: invalid ack cursor")
	// ErrChannelDisbanded rejects CMD pulls from a terminal source channel.
	ErrChannelDisbanded = errors.New("internal/usecase/cmdsync: channel disbanded")
	// ErrStateCursorDidNotAdvance prevents an infinite scan when a state store
	// reports another page without advancing its stable cursor.
	ErrStateCursorDidNotAdvance = errors.New("internal/usecase/cmdsync: state cursor did not advance")
)

// SyncQuery is the /message/sync request after access-layer validation.
type SyncQuery struct {
	// UID identifies the user whose durable CMD messages are synced.
	UID string
	// MessageSeq is accepted for legacy compatibility but does not select state.
	MessageSeq uint64
	// Limit bounds the number of CMD messages returned.
	Limit int
}

// SyncAckCommand is the /message/syncack request after access-layer validation.
type SyncAckCommand struct {
	// UID identifies the user acknowledging the latest CMD sync generation.
	UID string
	// LastMessageSeq is accepted for legacy compatibility but does not select channels.
	LastMessageSeq uint64
}

// BatchSyncQuery is the v3 command-message sync request.
type BatchSyncQuery struct {
	// UID identifies the user whose durable command messages are synced.
	UID string
	// DeviceFlag isolates native APP and desktop consumer progress.
	DeviceFlag uint8
	// LoginSessionID is the Link-U authenticated session bound into the S2S signature.
	LoginSessionID string
	// CredentialVersion fences stale sessions after login takeover or logout.
	CredentialVersion uint64
	// Limit bounds the number of messages returned in this batch.
	Limit int
}

// AckCursor identifies the exact per-command-channel sequence returned by a batch.
type AckCursor struct {
	// CommandChannelID is the durable command-channel id, including its command suffix.
	CommandChannelID string
	// ChannelType identifies the command channel namespace.
	ChannelType uint8
	// ThroughSeq is the highest sequence successfully exposed for this channel.
	ThroughSeq uint64
}

// BatchAckCommand acknowledges one explicit v3 command batch.
type BatchAckCommand struct {
	// UID identifies the owner of the command read cursors.
	UID string
	// DeviceFlag identifies the independent command consumer.
	DeviceFlag uint8
	// LoginSessionID is the signed Link-U session identifier.
	LoginSessionID string
	// CredentialVersion fences stale session acknowledgments.
	CredentialVersion uint64
	// BatchID is the deterministic digest returned by BatchSync.
	BatchID string
	// AckCursors are the exact per-channel frontiers covered by BatchID.
	AckCursors []AckCursor
}

// BindCommand creates or restores durable CMD discovery for a bounded recipient set and source channel.
type BindCommand struct {
	// UIDs binds a bounded recipient batch to one explicit source Channel.
	UIDs []string
	// Subscribers binds the exact normalized request-scoped recipient set.
	// It cannot be combined with UID, UIDs, ChannelID, or ChannelType.
	Subscribers []string
	UID         string
	ChannelID   string
	ChannelType uint8
}

// UnbindCommand tombstones durable CMD discovery for a bounded recipient set and source channel.
type UnbindCommand struct {
	// UIDs and Subscribers use the same mutually exclusive forms as BindCommand.
	UIDs        []string
	Subscribers []string
	UID         string
	ChannelID   string
	ChannelType uint8
}

// SyncedMessage is a command-channel message returned by CMD sync.
type SyncedMessage struct {
	// RedDot reports whether this command should affect its business badge domain.
	RedDot bool
	// MessageID is the globally unique durable message identifier.
	MessageID uint64
	// MessageSeq is the committed command-channel sequence.
	MessageSeq uint64
	// ChannelID is the client-facing source channel after suffix stripping.
	ChannelID string
	// ChannelType is the command-channel type.
	ChannelType uint8
	// FromUID identifies the sender user id.
	FromUID string
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server append timestamp used for deterministic ordering.
	ServerTimestampMS int64
	// SyncOnce reports whether this message is an explicit one-shot command-sync entry.
	SyncOnce bool
	// Payload is the immutable message payload returned to the access adapter.
	Payload []byte
}

// SyncResult contains durable CMD messages ready for response mapping.
type SyncResult struct {
	// Messages contains client-facing messages with one command suffix stripped.
	Messages []SyncedMessage
}

// BatchSyncResult contains a restart-safe v3 command-message batch.
type BatchSyncResult struct {
	// BatchID binds UID to the canonical explicit ACK cursor set.
	BatchID string
	// Messages contains client-facing command messages with one command suffix stripped.
	Messages []SyncedMessage
	// AckCursors contains the durable command-channel frontier for every returned channel.
	AckCursors []AckCursor
	// More reports that another batch remains after this one is acknowledged.
	More bool
}

// CommandChannelKey identifies one durable command channel log.
type CommandChannelKey struct {
	// ChannelID is the durable command-channel id, e.g. source____cmd.
	ChannelID string
	// ChannelType is the command-channel type.
	ChannelType uint8
}

// StateStore supplies the UID-owned CMD directory and persists acknowledgements.
type StateStore interface {
	ListUserCMDChannelMembershipPage(ctx context.Context, uid string, after metadb.UserCMDChannelMembershipCursor, limit int) ([]metadb.UserCMDChannelMembership, metadb.UserCMDChannelMembershipCursor, bool, error)
	UpsertUserCMDChannelMemberships(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error
	AdvanceUserCMDChannelMembershipAcks(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error
	TombstoneUserCMDChannelMemberships(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error
}

// MaxCommandReadBatch bounds one aligned multi-channel CMD read.
const MaxCommandReadBatch = 32

// CommandMessageRead carries one channel's durable recovery boundary.
type CommandMessageRead struct {
	Key     CommandChannelKey
	FromSeq uint64
	Limit   int
}

// CommandMessageReadResult preserves one source's result within an aligned batch.
type CommandMessageReadResult struct {
	// Messages contains this source's committed commands when Err is nil.
	Messages []SyncedMessage
	// Err identifies a terminal source or a read failure; global sync skips only
	// ErrChannelDisbanded and must fail for every unavailable or unknown result.
	Err error
}

// MessageBatchStore coalesces authoritative reads without changing ordering or
// acknowledgement semantics. Results align exactly with at most
// MaxCommandReadBatch inputs; an outer error invalidates the complete batch.
type MessageBatchStore interface {
	LoadCommandMessagesBatch(context.Context, []CommandMessageRead) ([]CommandMessageReadResult, error)
}

// DeviceStateStore supplies independent v3 command cursors by device class.
type DeviceStateStore interface {
	ListDeviceConversationActiveView(ctx context.Context, uid string, deviceFlag uint8, limit int) ([]metadb.ConversationState, error)
	UpsertCMDDeviceCursors(ctx context.Context, cursors []metadb.CMDDeviceCursor) error
}

// PrincipalStore reads authoritative device credentials used to fence CMD sync.
type PrincipalStore interface {
	GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error)
}

// MessageStore loads authoritative messages from command-channel logs.
type MessageStore interface {
	CommandChannelTail(ctx context.Context, key CommandChannelKey) (uint64, error)
	LoadCommandMessages(ctx context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]SyncedMessage, error)
}

// Options configures the CMD sync usecase.
type Options struct {
	// CommandChannelSuffix selects command IDs; empty retains the legacy default.
	CommandChannelSuffix string
	// States supplies CMD directory rows and persists acknowledgement progress.
	States StateStore
	// DeviceStates supplies v3 device-scoped command cursors.
	DeviceStates DeviceStateStore
	// Principals verifies the signed session tuple against durable authority.
	Principals PrincipalStore
	// Messages loads command-channel messages.
	Messages MessageStore
	// Records stores the latest unacknowledged sync generation per UID.
	Records *SyncRecordCache
	// Now supplies wall-clock time for deterministic tests.
	Now func() time.Time
	// ActiveScanLimit is the production store's active-index page size. The
	// cluster adapter continues paging until the full CMD view is exhausted.
	// ActiveScanLimit bounds each CMD membership page; Sync continues until the
	// stable UID directory is exhausted while retaining only the result limit.
	ActiveScanLimit int
	// DefaultLimit is used when SyncQuery.Limit is not positive.
	DefaultLimit int
	// MaxLimit caps SyncQuery.Limit and record retention per generation.
	MaxLimit int
}
