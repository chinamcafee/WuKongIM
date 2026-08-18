package cmdsync

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const (
	defaultActiveScanLimit = 2000
	defaultSyncLimit       = 200
	defaultMaxSyncLimit    = 10000
)

// App owns durable CMD sync and ack business rules.
type App struct {
	states          StateStore
	deviceStates    DeviceStateStore
	principals      PrincipalStore
	messages        MessageStore
	records         *SyncRecordCache
	now             func() time.Time
	activeScanLimit int
	defaultLimit    int
	maxLimit        int
}

// New creates a CMD sync app with safe defaults.
func New(opts Options) *App {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ActiveScanLimit <= 0 {
		opts.ActiveScanLimit = defaultActiveScanLimit
	}
	if opts.DefaultLimit <= 0 {
		opts.DefaultLimit = defaultSyncLimit
	}
	if opts.MaxLimit <= 0 {
		opts.MaxLimit = defaultMaxSyncLimit
	}
	if opts.DefaultLimit > opts.MaxLimit {
		opts.DefaultLimit = opts.MaxLimit
	}
	if opts.Records == nil {
		opts.Records = NewSyncRecordCache(SyncRecordCacheOptions{Now: opts.Now, MaxRecordsPerUID: opts.MaxLimit})
	}
	if opts.DeviceStates == nil {
		if store, ok := opts.States.(DeviceStateStore); ok {
			opts.DeviceStates = store
		}
	}
	if opts.Principals == nil {
		if store, ok := opts.States.(PrincipalStore); ok {
			opts.Principals = store
		}
	}
	return &App{
		states:          opts.States,
		deviceStates:    opts.DeviceStates,
		principals:      opts.Principals,
		messages:        opts.Messages,
		records:         opts.Records,
		now:             opts.Now,
		activeScanLimit: opts.ActiveScanLimit,
		defaultLimit:    opts.DefaultLimit,
		maxLimit:        opts.MaxLimit,
	}
}

// Sync loads durable command-channel messages and records the latest sync generation.
func (a *App) Sync(ctx context.Context, query SyncQuery) (SyncResult, error) {
	uid := strings.TrimSpace(query.UID)
	if uid == "" {
		return SyncResult{}, ErrUIDRequired
	}
	if a == nil || a.states == nil {
		return SyncResult{}, ErrStateStoreRequired
	}
	if a.messages == nil {
		return SyncResult{}, ErrMessageStoreRequired
	}
	limit := a.normalizeLimit(query.Limit)
	candidates, _, err := a.loadSyncCandidates(ctx, uid, limit)
	if err != nil {
		return SyncResult{}, err
	}

	result := SyncResult{Messages: make([]SyncedMessage, 0, len(candidates))}
	recordsByKey := make(map[CommandChannelKey]SyncRecord, len(candidates))
	for _, candidate := range candidates {
		msg := cloneSyncedMessage(candidate.message)
		if sourceID, ok := runtimechannelid.FromCommandChannel(msg.ChannelID); ok {
			msg.ChannelID = sourceID
		}
		result.Messages = append(result.Messages, msg)

		key := CommandChannelKey{ChannelID: candidate.commandChannelID, ChannelType: candidate.channelType}
		record := recordsByKey[key]
		record.CommandChannelID = key.ChannelID
		record.ChannelType = key.ChannelType
		if candidate.message.MessageSeq > record.LastReturnedMsgSeq {
			record.LastReturnedMsgSeq = candidate.message.MessageSeq
		}
		recordsByKey[key] = record
	}
	a.records.Replace(uid, syncRecordsFromMap(recordsByKey))
	return result, nil
}

// BatchSync returns a restart-safe v3 batch with explicit per-channel ACK cursors.
func (a *App) BatchSync(ctx context.Context, query BatchSyncQuery) (BatchSyncResult, error) {
	uid := strings.TrimSpace(query.UID)
	if uid == "" {
		return BatchSyncResult{}, ErrUIDRequired
	}
	if a == nil || a.states == nil {
		return BatchSyncResult{}, ErrStateStoreRequired
	}
	if a.deviceStates == nil {
		return BatchSyncResult{}, ErrDeviceStateStoreRequired
	}
	if err := a.verifyPrincipal(ctx, uid, query.DeviceFlag, query.LoginSessionID, query.CredentialVersion); err != nil {
		return BatchSyncResult{}, err
	}
	if a.messages == nil {
		return BatchSyncResult{}, ErrMessageStoreRequired
	}
	limit := a.normalizeLimit(query.Limit)
	candidates, more, err := a.loadDeviceSyncCandidates(ctx, uid, query.DeviceFlag, limit)
	if err != nil {
		return BatchSyncResult{}, err
	}
	result := BatchSyncResult{
		Messages: make([]SyncedMessage, 0, len(candidates)),
		More:     more,
	}
	recordsByKey := make(map[CommandChannelKey]SyncRecord, len(candidates))
	for _, candidate := range candidates {
		msg := cloneSyncedMessage(candidate.message)
		if sourceID, ok := runtimechannelid.FromCommandChannel(msg.ChannelID); ok {
			msg.ChannelID = sourceID
		}
		result.Messages = append(result.Messages, msg)
		key := CommandChannelKey{ChannelID: candidate.commandChannelID, ChannelType: candidate.channelType}
		record := recordsByKey[key]
		record.CommandChannelID = key.ChannelID
		record.ChannelType = key.ChannelType
		if candidate.message.MessageSeq > record.LastReturnedMsgSeq {
			record.LastReturnedMsgSeq = candidate.message.MessageSeq
		}
		recordsByKey[key] = record
	}
	records := syncRecordsFromMap(recordsByKey)
	result.AckCursors = ackCursorsFromRecords(records)
	if len(records) > 0 {
		result.BatchID = commandBatchID(uid, query.DeviceFlag, records)
	}
	return result, nil
}

// BatchAck advances exactly the explicit per-channel frontiers bound to BatchID.
func (a *App) BatchAck(ctx context.Context, cmd BatchAckCommand) error {
	uid := strings.TrimSpace(cmd.UID)
	if uid == "" {
		return ErrUIDRequired
	}
	if a == nil || a.states == nil {
		return ErrStateStoreRequired
	}
	if a.deviceStates == nil {
		return ErrDeviceStateStoreRequired
	}
	if err := a.verifyPrincipal(ctx, uid, cmd.DeviceFlag, cmd.LoginSessionID, cmd.CredentialVersion); err != nil {
		return err
	}
	if strings.TrimSpace(cmd.BatchID) == "" {
		return ErrBatchIDRequired
	}
	records, err := syncRecordsFromAckCursors(cmd.AckCursors)
	if err != nil {
		return err
	}
	expected := commandBatchID(uid, cmd.DeviceFlag, records)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(strings.TrimSpace(cmd.BatchID))) != 1 {
		return ErrBatchIDMismatch
	}
	return a.ackDeviceSyncRecords(ctx, uid, cmd.DeviceFlag, records)
}

// SyncAck advances read cursors for the latest sync generation only.
func (a *App) SyncAck(ctx context.Context, cmd SyncAckCommand) error {
	uid := strings.TrimSpace(cmd.UID)
	if uid == "" {
		return ErrUIDRequired
	}
	if a == nil || a.states == nil {
		return ErrStateStoreRequired
	}
	records := a.records.Peek(uid)
	if len(records) == 0 {
		return nil
	}
	validRecords := validSyncRecords(records)
	if len(validRecords) == 0 {
		a.records.DeleteIfUnchanged(uid, records)
		return nil
	}
	if err := a.ackSyncRecords(ctx, uid, validRecords); err != nil {
		return err
	}
	a.records.DeleteIfUnchanged(uid, records)
	return nil
}

func (a *App) loadSyncCandidates(ctx context.Context, uid string, limit int) ([]syncMessageCandidate, bool, error) {
	states, err := a.states.ListConversationActiveView(ctx, uid, a.activeScanLimit)
	if err != nil {
		return nil, false, err
	}
	channels := cmdSyncCandidatesFromStates(states)
	sortSyncChannelCandidates(channels)
	candidates := make([]syncMessageCandidate, 0, limit+1)
	perChannelLimit := limit + 1
	for _, candidate := range channels {
		key := candidate.key
		msgs, err := a.messages.LoadCommandMessages(ctx, key, candidate.readSeq+1, perChannelLimit)
		if err != nil {
			return nil, false, err
		}
		for _, msg := range msgs {
			candidates = append(candidates, syncMessageCandidate{
				commandChannelID: key.ChannelID,
				channelType:      key.ChannelType,
				message:          msg,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return syncMessageLess(candidates[i], candidates[j])
	})
	more := len(candidates) > limit
	if more {
		candidates = candidates[:limit]
	}
	return candidates, more, nil
}

func (a *App) loadDeviceSyncCandidates(ctx context.Context, uid string, deviceFlag uint8, limit int) ([]syncMessageCandidate, bool, error) {
	states, err := a.deviceStates.ListDeviceConversationActiveView(ctx, uid, deviceFlag, a.activeScanLimit)
	if err != nil {
		return nil, false, err
	}
	return a.loadSyncCandidatesFromStates(ctx, states, limit)
}

func (a *App) loadSyncCandidatesFromStates(ctx context.Context, states []metadb.ConversationState, limit int) ([]syncMessageCandidate, bool, error) {
	channels := cmdSyncCandidatesFromStates(states)
	sortSyncChannelCandidates(channels)
	candidates := make([]syncMessageCandidate, 0, limit+1)
	perChannelLimit := limit + 1
	for _, candidate := range channels {
		key := candidate.key
		msgs, err := a.messages.LoadCommandMessages(ctx, key, candidate.readSeq+1, perChannelLimit)
		if err != nil {
			return nil, false, err
		}
		for _, msg := range msgs {
			candidates = append(candidates, syncMessageCandidate{
				commandChannelID: key.ChannelID,
				channelType:      key.ChannelType,
				message:          msg,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return syncMessageLess(candidates[i], candidates[j])
	})
	more := len(candidates) > limit
	if more {
		candidates = candidates[:limit]
	}
	return candidates, more, nil
}

func (a *App) ackSyncRecords(ctx context.Context, uid string, records []SyncRecord) error {
	updatedAt := a.now().UnixNano()
	states := make([]metadb.ConversationState, 0, len(records))
	for _, record := range records {
		states = append(states, metadb.ConversationState{
			UID:         uid,
			Kind:        metadb.ConversationKindCMD,
			ChannelID:   record.CommandChannelID,
			ChannelType: int64(record.ChannelType),
			ReadSeq:     record.LastReturnedMsgSeq,
			UpdatedAt:   updatedAt,
		})
	}
	return a.states.UpsertConversationStates(ctx, states)
}

func (a *App) ackDeviceSyncRecords(ctx context.Context, uid string, deviceFlag uint8, records []SyncRecord) error {
	updatedAt := a.now().UnixNano()
	cursors := make([]metadb.CMDDeviceCursor, 0, len(records))
	for _, record := range records {
		cursors = append(cursors, metadb.CMDDeviceCursor{
			UID: uid, DeviceFlag: int64(deviceFlag), ChannelID: record.CommandChannelID,
			ChannelType: int64(record.ChannelType), ReadSeq: record.LastReturnedMsgSeq,
			ActiveAt: updatedAt, UpdatedAt: updatedAt,
		})
	}
	return a.deviceStates.UpsertCMDDeviceCursors(ctx, cursors)
}

func (a *App) verifyPrincipal(ctx context.Context, uid string, deviceFlag uint8, sessionID string, credentialVersion uint64) error {
	if a == nil || a.principals == nil {
		return ErrPrincipalStoreRequired
	}
	flag := protocolmeta.DeviceFlag(deviceFlag)
	if (flag != protocolmeta.DeviceFlagApp && flag != protocolmeta.DeviceFlagPC) ||
		strings.TrimSpace(sessionID) == "" || credentialVersion == 0 {
		return ErrPrincipalInvalid
	}
	device, err := a.principals.GetDevice(ctx, uid, int64(flag))
	if err != nil {
		return ErrPrincipalStale
	}
	if device.CredentialStatus != metadb.DeviceCredentialStatusActive ||
		device.CredentialVersion != credentialVersion ||
		device.LoginSessionID != strings.TrimSpace(sessionID) ||
		device.ExpiresAtUnixMS <= a.now().UnixMilli() {
		return ErrPrincipalStale
	}
	return nil
}

func ackCursorsFromRecords(records []SyncRecord) []AckCursor {
	if len(records) == 0 {
		return nil
	}
	out := make([]AckCursor, 0, len(records))
	for _, record := range records {
		out = append(out, AckCursor{
			CommandChannelID: record.CommandChannelID,
			ChannelType:      record.ChannelType,
			ThroughSeq:       record.LastReturnedMsgSeq,
		})
	}
	return out
}

func syncRecordsFromAckCursors(cursors []AckCursor) ([]SyncRecord, error) {
	if len(cursors) == 0 {
		return nil, ErrAckCursorInvalid
	}
	recordsByKey := make(map[CommandChannelKey]SyncRecord, len(cursors))
	for _, cursor := range cursors {
		channelID := strings.TrimSpace(cursor.CommandChannelID)
		if channelID == "" || cursor.ChannelType == 0 || cursor.ThroughSeq == 0 {
			return nil, ErrAckCursorInvalid
		}
		key := CommandChannelKey{ChannelID: channelID, ChannelType: cursor.ChannelType}
		if _, duplicate := recordsByKey[key]; duplicate {
			return nil, ErrAckCursorInvalid
		}
		recordsByKey[key] = SyncRecord{
			CommandChannelID:   channelID,
			ChannelType:        cursor.ChannelType,
			LastReturnedMsgSeq: cursor.ThroughSeq,
		}
	}
	return syncRecordsFromMap(recordsByKey), nil
}

func commandBatchID(uid string, deviceFlag uint8, records []SyncRecord) string {
	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "%d:%s|%d|", len(uid), uid, deviceFlag)
	for _, record := range records {
		_, _ = fmt.Fprintf(digest, "%d:%s|%d|%d;", len(record.CommandChannelID), record.CommandChannelID, record.ChannelType, record.LastReturnedMsgSeq)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func (a *App) normalizeLimit(limit int) int {
	if limit <= 0 {
		return a.defaultLimit
	}
	if limit > a.maxLimit {
		return a.maxLimit
	}
	return limit
}

type syncChannelCandidate struct {
	key      CommandChannelKey
	readSeq  uint64
	activeAt int64
}

type syncMessageCandidate struct {
	commandChannelID string
	channelType      uint8
	message          SyncedMessage
}

func cmdSyncCandidatesFromStates(states []metadb.ConversationState) []syncChannelCandidate {
	candidates := make([]syncChannelCandidate, 0, len(states))
	for _, state := range states {
		if state.Kind != metadb.ConversationKindCMD || state.ChannelID == "" || state.ChannelType <= 0 || state.ChannelType > 255 {
			continue
		}
		candidates = append(candidates, syncChannelCandidate{
			key:      CommandChannelKey{ChannelID: state.ChannelID, ChannelType: uint8(state.ChannelType)},
			readSeq:  maxUint64(state.ReadSeq, state.DeletedToSeq),
			activeAt: state.ActiveAt,
		})
	}
	return candidates
}

func sortSyncChannelCandidates(candidates []syncChannelCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].activeAt != candidates[j].activeAt {
			return candidates[i].activeAt > candidates[j].activeAt
		}
		if candidates[i].key.ChannelType != candidates[j].key.ChannelType {
			return candidates[i].key.ChannelType < candidates[j].key.ChannelType
		}
		return candidates[i].key.ChannelID < candidates[j].key.ChannelID
	})
}

func syncMessageLess(left, right syncMessageCandidate) bool {
	if left.message.ServerTimestampMS != right.message.ServerTimestampMS {
		return left.message.ServerTimestampMS < right.message.ServerTimestampMS
	}
	if left.commandChannelID != right.commandChannelID {
		return left.commandChannelID < right.commandChannelID
	}
	if left.channelType != right.channelType {
		return left.channelType < right.channelType
	}
	if left.message.MessageSeq != right.message.MessageSeq {
		return left.message.MessageSeq < right.message.MessageSeq
	}
	return left.message.MessageID < right.message.MessageID
}

func syncRecordsFromMap(recordsByKey map[CommandChannelKey]SyncRecord) []SyncRecord {
	if len(recordsByKey) == 0 {
		return nil
	}
	keys := make([]CommandChannelKey, 0, len(recordsByKey))
	for key := range recordsByKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ChannelType != keys[j].ChannelType {
			return keys[i].ChannelType < keys[j].ChannelType
		}
		return keys[i].ChannelID < keys[j].ChannelID
	})
	records := make([]SyncRecord, 0, len(keys))
	for _, key := range keys {
		records = append(records, recordsByKey[key])
	}
	return records
}

func validSyncRecords(records []SyncRecord) []SyncRecord {
	valid := make([]SyncRecord, 0, len(records))
	for _, record := range records {
		if record.LastReturnedMsgSeq == 0 || strings.TrimSpace(record.CommandChannelID) == "" || record.ChannelType == 0 {
			continue
		}
		valid = append(valid, record)
	}
	return valid
}

func cloneSyncedMessage(msg SyncedMessage) SyncedMessage {
	msg.Payload = append([]byte(nil), msg.Payload...)
	return msg
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}
