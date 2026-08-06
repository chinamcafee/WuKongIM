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
	// ErrStateStoreRequired reports a missing durable CMD state dependency.
	ErrStateStoreRequired = errors.New("internal/usecase/cmdsync: state store required")
	// ErrMessageStoreRequired reports a missing command-channel message dependency.
	ErrMessageStoreRequired = errors.New("internal/usecase/cmdsync: message store required")
	// ErrBatchIDRequired reports a missing v3 command batch identifier.
	ErrBatchIDRequired = errors.New("internal/usecase/cmdsync: batch id required")
	// ErrBatchIDMismatch reports that an ACK does not match its explicit channel cursors.
	ErrBatchIDMismatch = errors.New("internal/usecase/cmdsync: batch id mismatch")
	// ErrAckCursorInvalid reports an invalid or duplicate v3 command ACK cursor.
	ErrAckCursorInvalid = errors.New("internal/usecase/cmdsync: invalid ack cursor")
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
	// BatchID is the deterministic digest returned by BatchSync.
	BatchID string
	// AckCursors are the exact per-channel frontiers covered by BatchID.
	AckCursors []AckCursor
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

// StateStore supplies CMD-kind conversation state from the unified projection.
type StateStore interface {
	ListConversationActiveView(ctx context.Context, uid string, limit int) ([]metadb.ConversationState, error)
	UpsertConversationStates(ctx context.Context, states []metadb.ConversationState) error
}

// MessageStore loads authoritative messages from command-channel logs.
type MessageStore interface {
	LoadCommandMessages(ctx context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]SyncedMessage, error)
}

// Options configures the CMD sync usecase.
type Options struct {
	// States supplies CMD-kind unified conversation rows and persists read progress.
	States StateStore
	// Messages loads command-channel messages.
	Messages MessageStore
	// Records stores the latest unacknowledged sync generation per UID.
	Records *SyncRecordCache
	// Now supplies wall-clock time for deterministic tests.
	Now func() time.Time
	// ActiveScanLimit is the production store's active-index page size. The
	// cluster adapter continues paging until the full CMD view is exhausted.
	ActiveScanLimit int
	// DefaultLimit is used when SyncQuery.Limit is not positive.
	DefaultLimit int
	// MaxLimit caps SyncQuery.Limit and record retention per generation.
	MaxLimit int
}
