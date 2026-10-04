package cmdsync

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	protocolmeta "github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	"sort"
	"strings"
	"time"

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
	// commandChannels applies the deployment suffix without process-global state.
	commandChannels runtimechannelid.CommandCodec
	deviceStates    DeviceStateStore
	principals      PrincipalStore
	states          StateStore
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
	return &App{
		commandChannels: runtimechannelid.CommandCodec{Suffix: opts.CommandChannelSuffix},
		deviceStates:    opts.DeviceStates,
		principals:      opts.Principals,
		states:          opts.States,
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
	candidates := make([]syncMessageCandidate, 0, limit)
	cursor := metadb.UserCMDChannelMembershipCursor{}
	for {
		memberships, nextCursor, done, err := a.states.ListUserCMDChannelMembershipPage(ctx, uid, cursor, a.activeScanLimit)
		if err != nil {
			return SyncResult{}, err
		}
		channels := cmdSyncCandidatesFromMemberships(memberships)
		sortSyncChannelCandidates(channels)
		for start := 0; start < len(channels); start += MaxCommandReadBatch {
			end := min(start+MaxCommandReadBatch, len(channels))
			batch := channels[start:end]
			messages, err := a.loadCommandBatch(ctx, batch, limit)
			if err != nil {
				return SyncResult{}, err
			}
			for i, candidate := range batch {
				for _, msg := range messages[i] {
					candidates = append(candidates, syncMessageCandidate{commandChannelID: candidate.key.ChannelID, channelType: candidate.key.ChannelType, message: msg})
				}
			}
			candidates = trimSyncMessageCandidates(candidates, limit)
		}

		if done {
			break
		}
		if nextCursor == cursor {
			return SyncResult{}, ErrStateCursorDidNotAdvance
		}
		cursor = nextCursor
	}

	result := SyncResult{Messages: make([]SyncedMessage, 0, len(candidates))}
	recordsByKey := make(map[CommandChannelKey]SyncRecord, len(candidates))
	for _, candidate := range candidates {
		msg := cloneSyncedMessage(candidate.message)
		if sourceID, ok := a.commandChannels.FromCommandChannel(msg.ChannelID); ok {
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
	updatedAt := a.now().UnixNano()
	validRecords := validSyncRecords(records)
	if len(validRecords) == 0 {
		a.records.DeleteIfUnchanged(uid, records)
		return nil
	}

	memberships := make([]metadb.UserCMDChannelMembership, 0, len(validRecords))
	for _, record := range validRecords {
		memberships = append(memberships, metadb.UserCMDChannelMembership{
			UID:              uid,
			CommandChannelID: record.CommandChannelID,
			ChannelType:      int64(record.ChannelType),
			AckSeq:           record.LastReturnedMsgSeq,
			UpdatedAt:        updatedAt,
		})
	}
	if err := a.states.AdvanceUserCMDChannelMembershipAcks(ctx, memberships); err != nil {
		return err
	}
	a.records.DeleteIfUnchanged(uid, records)
	return nil
}

func validateBindingIdentity(uid, channelID string, channelType uint8) (string, string, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return "", "", ErrUIDRequired
	}
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return "", "", ErrChannelRequired
	}
	if channelType == 0 {
		return "", "", ErrChannelTypeRequired
	}
	return uid, channelID, nil
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
	key     CommandChannelKey
	fromSeq uint64
}

type syncMessageCandidate struct {
	commandChannelID string
	channelType      uint8
	message          SyncedMessage
}

func cmdSyncCandidatesFromMemberships(memberships []metadb.UserCMDChannelMembership) []syncChannelCandidate {
	candidates := make([]syncChannelCandidate, 0, len(memberships))
	for _, membership := range memberships {
		if membership.Tombstone || membership.CommandChannelID == "" || membership.ChannelType <= 0 || membership.ChannelType > 255 || membership.AckSeq == ^uint64(0) {
			continue
		}
		fromSeq := membership.StartSeq
		if fromSeq == 0 {
			fromSeq = 1
		}
		if ackNext := membership.AckSeq + 1; ackNext > fromSeq {
			fromSeq = ackNext
		}
		candidates = append(candidates, syncChannelCandidate{
			key:     CommandChannelKey{ChannelID: membership.CommandChannelID, ChannelType: uint8(membership.ChannelType)},
			fromSeq: fromSeq,
		})
	}
	return candidates
}

func sortSyncChannelCandidates(candidates []syncChannelCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].key.ChannelID != candidates[j].key.ChannelID {
			return candidates[i].key.ChannelID < candidates[j].key.ChannelID
		}
		return candidates[i].key.ChannelType < candidates[j].key.ChannelType
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

func trimSyncMessageCandidates(candidates []syncMessageCandidate, limit int) []syncMessageCandidate {
	sort.Slice(candidates, func(i, j int) bool {
		return syncMessageLess(candidates[i], candidates[j])
	})
	if len(candidates) > limit {
		clear(candidates[limit:])
		return candidates[:limit]
	}
	return candidates
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
		msgs, err := a.messages.LoadCommandMessages(ctx, key, candidate.fromSeq, perChannelLimit)
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

func cmdSyncCandidatesFromStates(states []metadb.ConversationState) []syncChannelCandidate {
	candidates := make([]syncChannelCandidate, 0, len(states))
	for _, state := range states {
		if state.Kind != metadb.ConversationKindCMD || state.ChannelID == "" || state.ChannelType <= 0 || state.ChannelType > 255 {
			continue
		}
		candidates = append(candidates, syncChannelCandidate{
			key:     CommandChannelKey{ChannelID: state.ChannelID, ChannelType: uint8(state.ChannelType)},
			fromSeq: max(state.ReadSeq, state.DeletedToSeq) + 1,
		})
	}
	return candidates
}
