package cluster

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const cmdSyncReadPageLimit = 256
const cmdSyncActivePageLimit = 2000
const cmdSyncMaxActivePages = 10000

// CMDSyncNode exposes cluster reads and writes needed by CMD sync.
type CMDSyncNode interface {
	ListConversationActivePage(context.Context, metadb.ConversationKind, string, metadb.ConversationActiveCursor, int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
	UpsertConversationStatesBatch(context.Context, []metadb.ConversationState) error
	GetCMDDeviceCursorsBatch(context.Context, []metadb.CMDDeviceCursorKey) (map[metadb.CMDDeviceCursorKey]metadb.CMDDeviceCursor, error)
	UpsertCMDDeviceCursorsBatch(context.Context, []metadb.CMDDeviceCursor) error
	GetDeviceMetadata(context.Context, string, int64) (metadb.Device, error)
	ReadChannelCommitted(context.Context, channelruntime.ChannelID, channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error)
}

// CMDSyncStore adapts cluster unified conversation projection rows to CMD sync.
type CMDSyncStore struct {
	node CMDSyncNode
}

var _ cmdsync.StateStore = (*CMDSyncStore)(nil)
var _ cmdsync.DeviceStateStore = (*CMDSyncStore)(nil)
var _ cmdsync.PrincipalStore = (*CMDSyncStore)(nil)
var _ cmdsync.MessageStore = (*CMDSyncStore)(nil)

// NewCMDSyncStore creates a cluster-backed CMD sync store.
func NewCMDSyncStore(node CMDSyncNode) *CMDSyncStore {
	return &CMDSyncStore{node: node}
}

// ListDeviceConversationActiveView overlays one device class's cursor floors on
// the UID-level CMD channel discovery view.
func (s *CMDSyncStore) ListDeviceConversationActiveView(ctx context.Context, uid string, deviceFlag uint8, limit int) ([]metadb.ConversationState, error) {
	rows, err := s.ListConversationActiveView(ctx, uid, limit)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	keys := make([]metadb.CMDDeviceCursorKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, metadb.CMDDeviceCursorKey{
			UID: uid, DeviceFlag: int64(deviceFlag), ChannelID: row.ChannelID, ChannelType: row.ChannelType,
		})
	}
	cursors, err := s.node.GetCMDDeviceCursorsBatch(ctx, keys)
	if err != nil {
		return nil, err
	}
	for i, key := range keys {
		cursor, ok := cursors[key]
		if !ok {
			rows[i].ReadSeq = 0
			rows[i].DeletedToSeq = 0
			continue
		}
		rows[i].ReadSeq = cursor.ReadSeq
		rows[i].DeletedToSeq = cursor.DeletedToSeq
	}
	return rows, nil
}

// UpsertCMDDeviceCursors advances independent device-class command progress.
func (s *CMDSyncStore) UpsertCMDDeviceCursors(ctx context.Context, cursors []metadb.CMDDeviceCursor) error {
	if len(cursors) == 0 {
		return nil
	}
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.UpsertCMDDeviceCursorsBatch(ctx, append([]metadb.CMDDeviceCursor(nil), cursors...))
}

// GetDevice reads the durable credential authority for principal fencing.
func (s *CMDSyncStore) GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	if s == nil || s.node == nil {
		return metadb.Device{}, metadb.ErrNotFound
	}
	return s.node.GetDeviceMetadata(ctx, uid, deviceFlag)
}

// ListConversationActiveView reads the UID-owned CMD projection view.
func (s *CMDSyncStore) ListConversationActiveView(ctx context.Context, uid string, limit int) ([]metadb.ConversationState, error) {
	if s == nil || s.node == nil {
		return nil, metadb.ErrNotFound
	}
	if limit <= 0 {
		limit = cmdSyncActivePageLimit
	}
	rows := make([]metadb.ConversationState, 0, limit)
	cursor := metadb.ConversationActiveCursor{}
	for page := 0; page < cmdSyncMaxActivePages; page++ {
		pageRows, nextCursor, done, err := s.node.ListConversationActivePage(
			ctx, metadb.ConversationKindCMD, uid, cursor, limit,
		)
		if err != nil {
			return nil, err
		}
		rows = append(rows, pageRows...)
		if done {
			return cloneConversationStates(rows), nil
		}
		if nextCursor == (metadb.ConversationActiveCursor{}) || nextCursor == cursor {
			return nil, fmt.Errorf("internal/infra/cluster: cmd active cursor did not advance")
		}
		cursor = nextCursor
	}
	return nil, fmt.Errorf("internal/infra/cluster: cmd active scan exceeded %d pages", cmdSyncMaxActivePages)
}

// UpsertConversationStates advances CMD-kind read state in the unified projection.
func (s *CMDSyncStore) UpsertConversationStates(ctx context.Context, states []metadb.ConversationState) error {
	if len(states) == 0 {
		return nil
	}
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	cloned := cloneConversationStates(states)
	for i := range cloned {
		cloned[i].Kind = metadb.ConversationKindCMD
	}
	return s.node.UpsertConversationStatesBatch(ctx, cloned)
}

// LoadCommandMessages reads committed messages from one command-channel log.
func (s *CMDSyncStore) LoadCommandMessages(ctx context.Context, key cmdsync.CommandChannelKey, fromSeq uint64, limit int) ([]cmdsync.SyncedMessage, error) {
	if s == nil || s.node == nil {
		return nil, metadb.ErrNotFound
	}
	if limit <= 0 {
		limit = 1
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	out := make([]cmdsync.SyncedMessage, 0, limit)
	nextSeq := fromSeq
	for len(out) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		read, err := s.node.ReadChannelCommitted(ctx, channelruntime.ChannelID{ID: key.ChannelID, Type: key.ChannelType}, channelstore.ReadCommittedRequest{
			FromSeq:  nextSeq,
			MaxSeq:   maxUint64(),
			Limit:    cmdSyncReadPageLimit,
			MaxBytes: maxInt(),
		})
		if err != nil {
			return nil, mapAppendError(err)
		}
		if len(read.Messages) == 0 {
			break
		}
		for _, msg := range read.Messages {
			if !isCommandSyncChannelMessage(msg) {
				continue
			}
			out = append(out, cmdSyncedMessageFromChannel(msg))
			if len(out) >= limit {
				break
			}
		}
		if read.NextSeq <= nextSeq {
			break
		}
		nextSeq = read.NextSeq
	}
	return out, nil
}

func isCommandSyncChannelMessage(msg channelruntime.Message) bool {
	return msg.SyncOnce || runtimechannelid.IsCommandChannel(msg.ChannelID)
}

func cmdSyncedMessageFromChannel(msg channelruntime.Message) cmdsync.SyncedMessage {
	return cmdsync.SyncedMessage{
		MessageID:         msg.MessageID,
		MessageSeq:        msg.MessageSeq,
		ChannelID:         msg.ChannelID,
		ChannelType:       msg.ChannelType,
		FromUID:           msg.FromUID,
		ClientMsgNo:       msg.ClientMsgNo,
		ServerTimestampMS: msg.ServerTimestampMS,
		RedDot:            msg.RedDot,
		SyncOnce:          msg.SyncOnce || runtimechannelid.IsCommandChannel(msg.ChannelID),
		Payload:           append([]byte(nil), msg.Payload...),
	}
}
