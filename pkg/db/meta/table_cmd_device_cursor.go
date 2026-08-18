package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	cmdDeviceCursorColumnUID          uint16 = 1
	cmdDeviceCursorColumnDeviceFlag   uint16 = 2
	cmdDeviceCursorColumnChannelID    uint16 = 3
	cmdDeviceCursorColumnChannelType  uint16 = 4
	cmdDeviceCursorColumnReadSeq      uint16 = 5
	cmdDeviceCursorColumnDeletedToSeq uint16 = 6
	cmdDeviceCursorColumnActiveAt     uint16 = 7
	cmdDeviceCursorColumnUpdatedAt    uint16 = 8
)

// CMDDeviceCursor stores one device class's independent command-channel progress.
// Discovery remains in the UID-level CMD conversation projection so a newly
// admitted device can start at sequence zero without missing retained commands.
type CMDDeviceCursor struct {
	UID          string
	DeviceFlag   int64
	ChannelID    string
	ChannelType  int64
	ReadSeq      uint64
	DeletedToSeq uint64
	ActiveAt     int64
	UpdatedAt    int64
}

// CMDDeviceCursorKey identifies one device-scoped command-channel cursor.
type CMDDeviceCursorKey struct {
	UID         string
	DeviceFlag  int64
	ChannelID   string
	ChannelType int64
}

var cmdDeviceCursorTable = registerMetaTable(TableSpec[CMDDeviceCursor]{
	ID:   TableIDCMDDeviceCursor,
	Name: "cmd_device_cursor",
	Columns: []schema.Column{
		{ID: cmdDeviceCursorColumnUID, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: cmdDeviceCursorColumnDeviceFlag, Name: "device_flag", Type: schema.TypeInt64, Required: true},
		{ID: cmdDeviceCursorColumnChannelID, Name: "command_channel_id", Type: schema.TypeString, Required: true},
		{ID: cmdDeviceCursorColumnChannelType, Name: "channel_type", Type: schema.TypeInt64, Required: true},
		{ID: cmdDeviceCursorColumnReadSeq, Name: "read_seq", Type: schema.TypeUint64},
		{ID: cmdDeviceCursorColumnDeletedToSeq, Name: "deleted_to_seq", Type: schema.TypeUint64},
		{ID: cmdDeviceCursorColumnActiveAt, Name: "active_at", Type: schema.TypeInt64},
		{ID: cmdDeviceCursorColumnUpdatedAt, Name: "updated_at", Type: schema.TypeInt64},
	},
	Families: []schema.Family{{ID: cmdDeviceCursorPrimaryFamilyID, Name: "primary", Columns: []uint16{
		cmdDeviceCursorColumnReadSeq,
		cmdDeviceCursorColumnDeletedToSeq,
		cmdDeviceCursorColumnActiveAt,
		cmdDeviceCursorColumnUpdatedAt,
	}}},
	Primary: PrimarySpec[CMDDeviceCursor]{
		IndexID:  cmdDeviceCursorPrimaryIndexID,
		FamilyID: cmdDeviceCursorPrimaryFamilyID,
		Name:     "pk_cmd_device_cursor",
		Columns:  []uint16{cmdDeviceCursorColumnUID, cmdDeviceCursorColumnDeviceFlag, cmdDeviceCursorColumnChannelID, cmdDeviceCursorColumnChannelType},
		Layout:   KeyLayout{KeyString, KeyInt64Ordered, KeyString, KeyInt64Ordered},
		Key: func(cursor CMDDeviceCursor) KeyParts {
			return cmdDeviceCursorPrimaryKey(cursor.UID, cursor.DeviceFlag, cursor.ChannelID, cursor.ChannelType)
		},
	},
	Validate: validateCMDDeviceCursor,
	EncodeValue: func(cursor CMDDeviceCursor) ([]byte, error) {
		return encodeCMDDeviceCursorValue(cursor), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (CMDDeviceCursor, error) {
		return decodeCMDDeviceCursorValue(primary[0].S, primary[1].I64, primary[2].S, primary[3].I64, value)
	},
})

// CMDDeviceCursorTable describes the device-scoped command cursor schema.
var CMDDeviceCursorTable = cmdDeviceCursorTable.Schema()

// GetCMDDeviceCursor returns one device-scoped command cursor.
func (s *Shard) GetCMDDeviceCursor(ctx context.Context, key CMDDeviceCursorKey) (CMDDeviceCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return CMDDeviceCursor{}, false, err
	}
	if err := validateCMDDeviceCursorKey(key); err != nil {
		return CMDDeviceCursor{}, false, err
	}
	return cmdDeviceCursorTable.Get(ctx, s, cmdDeviceCursorPrimaryKey(key.UID, key.DeviceFlag, key.ChannelID, key.ChannelType))
}

// UpsertCMDDeviceCursor advances cursor fields monotonically.
func (s *Shard) UpsertCMDDeviceCursor(ctx context.Context, cursor CMDDeviceCursor) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateCMDDeviceCursor(cursor); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	pk := cmdDeviceCursorPrimaryKey(cursor.UID, cursor.DeviceFlag, cursor.ChannelID, cursor.ChannelType)
	primaryKey, err := cmdDeviceCursorTable.primaryRowKey(s.hashSlot, pk)
	if err != nil {
		return err
	}
	existing, exists, err := cmdDeviceCursorTable.getByPrimaryKey(s.db, s.hashSlot, pk)
	if err != nil {
		return err
	}
	next := mergeCMDDeviceCursor(existing, exists, cursor)
	value := encodeCMDDeviceCursorValue(next)
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(primaryKey, value); err != nil {
		return err
	}
	return batch.Commit(true)
}

// UpsertCMDDeviceCursor stages a monotonic device-scoped cursor update.
func (b *Batch) UpsertCMDDeviceCursor(hashSlot HashSlot, cursor CMDDeviceCursor) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if err := validateCMDDeviceCursor(cursor); err != nil {
		return err
	}
	pk := cmdDeviceCursorPrimaryKey(cursor.UID, cursor.DeviceFlag, cursor.ChannelID, cursor.ChannelType)
	primaryKey, err := cmdDeviceCursorTable.primaryRowKey(hashSlot, pk)
	if err != nil {
		return err
	}
	b.addOp(hashSlot, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		existing, exists, err := cmdDeviceCursorTable.loadBatchRow(state, hashSlot, pk, primaryKey)
		if err != nil {
			return err
		}
		next := mergeCMDDeviceCursor(existing, exists, cursor)
		value := encodeCMDDeviceCursorValue(next)
		if err := batch.Set(primaryKey, value); err != nil {
			return err
		}
		state.tableRows[string(primaryKey)] = tableRowOverlay{value: append([]byte(nil), value...), exists: true}
		return nil
	})
	return nil
}

func cmdDeviceCursorPrimaryKey(uid string, deviceFlag int64, channelID string, channelType int64) KeyParts {
	return KeyParts{String(uid), Int64Ordered(deviceFlag), String(channelID), Int64Ordered(channelType)}
}

func validateCMDDeviceCursorKey(key CMDDeviceCursorKey) error {
	if err := validateKeyString(key.UID); err != nil {
		return err
	}
	if key.DeviceFlag < 0 || key.DeviceFlag > 255 {
		return dberrors.ErrInvalidArgument
	}
	return validateConversationKey(ConversationKey{ChannelID: key.ChannelID, ChannelType: key.ChannelType})
}

func validateCMDDeviceCursor(cursor CMDDeviceCursor) error {
	return validateCMDDeviceCursorKey(CMDDeviceCursorKey{
		UID: cursor.UID, DeviceFlag: cursor.DeviceFlag,
		ChannelID: cursor.ChannelID, ChannelType: cursor.ChannelType,
	})
}

func mergeCMDDeviceCursor(existing CMDDeviceCursor, exists bool, next CMDDeviceCursor) CMDDeviceCursor {
	if !exists {
		return next
	}
	if next.ReadSeq > existing.ReadSeq {
		existing.ReadSeq = next.ReadSeq
	}
	if next.DeletedToSeq > existing.DeletedToSeq {
		existing.DeletedToSeq = next.DeletedToSeq
	}
	if next.ActiveAt > existing.ActiveAt {
		existing.ActiveAt = next.ActiveAt
	}
	if next.UpdatedAt > existing.UpdatedAt {
		existing.UpdatedAt = next.UpdatedAt
	}
	return existing
}

func encodeCMDDeviceCursorValue(cursor CMDDeviceCursor) []byte {
	value := appendValueUint64(nil, cursor.ReadSeq)
	value = appendValueUint64(value, cursor.DeletedToSeq)
	value = appendValueInt64(value, cursor.ActiveAt)
	return appendValueInt64(value, cursor.UpdatedAt)
}

func decodeCMDDeviceCursorValue(uid string, deviceFlag int64, channelID string, channelType int64, value []byte) (CMDDeviceCursor, error) {
	readSeq, rest, err := readValueUint64(value)
	if err != nil {
		return CMDDeviceCursor{}, err
	}
	deletedToSeq, rest, err := readValueUint64(rest)
	if err != nil {
		return CMDDeviceCursor{}, err
	}
	activeAt, rest, err := readValueInt64(rest)
	if err != nil {
		return CMDDeviceCursor{}, err
	}
	updatedAt, rest, err := readValueInt64(rest)
	if err != nil || len(rest) != 0 {
		return CMDDeviceCursor{}, dberrors.ErrCorruptValue
	}
	return CMDDeviceCursor{
		UID: uid, DeviceFlag: deviceFlag, ChannelID: channelID, ChannelType: channelType,
		ReadSeq: readSeq, DeletedToSeq: deletedToSeq, ActiveAt: activeAt, UpdatedAt: updatedAt,
	}, nil
}
