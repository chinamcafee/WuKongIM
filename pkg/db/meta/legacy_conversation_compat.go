package meta

// UserConversationActiveHint describes a hot conversation activity hint.
type UserConversationActiveHint struct {
	// UID identifies the user that owns the conversation state.
	UID string
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ActiveAt is the candidate activity timestamp.
	ActiveAt int64
	// MessageSeq fences stale activity hints after a user delete barrier.
	MessageSeq uint64
	// SparseActive is the requested sparse-active mode when SparseActiveSet is true.
	SparseActive bool
	// SparseActiveSet reports that SparseActive should update the stored sparse mode.
	SparseActiveSet bool
}

// UserConversationState is the legacy ordinary conversation source shape.
// It maps to ConversationState with ConversationKindNormal.
type UserConversationState struct {
	// UID identifies the user that owns the conversation state.
	UID string
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ReadSeq is the highest message sequence acknowledged by the user.
	ReadSeq uint64
	// DeletedToSeq is the highest message sequence hidden from future sync.
	DeletedToSeq uint64
	// ActiveAt is the latest activity timestamp used by active scans.
	ActiveAt int64
	// UpdatedAt records the latest state mutation timestamp.
	UpdatedAt int64
	// SparseActive reports that ActiveAt is a low-frequency ordering anchor.
	SparseActive bool
}

// UserConversationActivePatch is the legacy ordinary active patch shape.
// It maps to ConversationActivePatch with ConversationKindNormal.
type UserConversationActivePatch struct {
	// UID identifies the user that owns the conversation state.
	UID string
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ReadSeq is a monotonic read floor merged with the durable row.
	ReadSeq uint64
	// DeletedToSeq is a monotonic visibility floor merged with the durable row.
	DeletedToSeq uint64
	// ActiveAt is the candidate activity timestamp.
	ActiveAt int64
	// UpdatedAt records the latest projection update timestamp.
	UpdatedAt int64
	// MessageSeq fences stale activity hints after a user delete barrier.
	MessageSeq uint64
	// SparseActive is the requested sparse-active mode when SparseActiveSet is true.
	SparseActive bool
	// SparseActiveSet reports that SparseActive should update the stored sparse mode.
	SparseActiveSet bool
}

// UserConversationDelete is the legacy ordinary delete request shape.
// It maps to ConversationDelete with ConversationKindNormal.
type UserConversationDelete struct {
	// UID identifies the user that owns the conversation state.
	UID string
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// DeletedToSeq is the highest sequence hidden by the delete.
	DeletedToSeq uint64
	// UpdatedAt records when the hide operation was requested.
	UpdatedAt int64
}

// ConversationDeleteBarrier prevents stale hints from reactivating deletes.
type ConversationDeleteBarrier struct {
	UID          string
	ChannelID    string
	ChannelType  int64
	DeletedToSeq uint64
}

// UserConversationDeleteBarrier is the legacy ordinary delete-barrier shape.
type UserConversationDeleteBarrier = ConversationDeleteBarrier

// CMDConversationState is the legacy command sync cursor shape.
// It maps to ConversationState with ConversationKindCMD.
type CMDConversationState struct {
	// UID identifies the user that owns the CMD sync cursor.
	UID string
	// ChannelID identifies the command or SyncOnce source channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ReadSeq is the highest command message sequence acknowledged by the user.
	ReadSeq uint64
	// DeletedToSeq is the highest command message sequence hidden from sync.
	DeletedToSeq uint64
	// ActiveAt is the latest command activity timestamp used by active scans.
	ActiveAt int64
	// UpdatedAt records the latest cursor mutation timestamp.
	UpdatedAt int64
}

// CMDConversationReadPatch advances one legacy CMD read cursor.
// It maps to ConversationActivePatch with ConversationKindCMD.
type CMDConversationReadPatch struct {
	// UID identifies the user that owns the CMD sync cursor.
	UID string
	// ChannelID identifies the command or SyncOnce source channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ReadSeq is the monotonic read floor.
	ReadSeq uint64
	// UpdatedAt records when the read cursor advanced.
	UpdatedAt int64
}

func userConversationStateToConversation(state UserConversationState) ConversationState {
	return ConversationState{
		UID:          state.UID,
		Kind:         ConversationKindNormal,
		ChannelID:    state.ChannelID,
		ChannelType:  state.ChannelType,
		ReadSeq:      state.ReadSeq,
		DeletedToSeq: state.DeletedToSeq,
		ActiveAt:     state.ActiveAt,
		UpdatedAt:    state.UpdatedAt,
		SparseActive: state.SparseActive,
	}
}

func userConversationStateFromConversation(state ConversationState) UserConversationState {
	return UserConversationState{
		UID:          state.UID,
		ChannelID:    state.ChannelID,
		ChannelType:  state.ChannelType,
		ReadSeq:      state.ReadSeq,
		DeletedToSeq: state.DeletedToSeq,
		ActiveAt:     state.ActiveAt,
		UpdatedAt:    state.UpdatedAt,
		SparseActive: state.SparseActive,
	}
}

func userConversationStatesFromConversations(states []ConversationState) []UserConversationState {
	out := make([]UserConversationState, 0, len(states))
	for _, state := range states {
		out = append(out, userConversationStateFromConversation(state))
	}
	return out
}

func userConversationActivePatchToConversation(patch UserConversationActivePatch) ConversationActivePatch {
	return ConversationActivePatch{
		UID:             patch.UID,
		Kind:            ConversationKindNormal,
		ChannelID:       patch.ChannelID,
		ChannelType:     patch.ChannelType,
		ReadSeq:         patch.ReadSeq,
		DeletedToSeq:    patch.DeletedToSeq,
		ActiveAt:        patch.ActiveAt,
		UpdatedAt:       patch.UpdatedAt,
		MessageSeq:      patch.MessageSeq,
		SparseActive:    patch.SparseActive,
		SparseActiveSet: patch.SparseActiveSet,
	}
}

func userConversationActivePatchesToConversations(patches []UserConversationActivePatch) []ConversationActivePatch {
	out := make([]ConversationActivePatch, 0, len(patches))
	for _, patch := range patches {
		out = append(out, userConversationActivePatchToConversation(patch))
	}
	return out
}

func userConversationDeleteToConversation(req UserConversationDelete) ConversationDelete {
	return ConversationDelete{
		UID:          req.UID,
		Kind:         ConversationKindNormal,
		ChannelID:    req.ChannelID,
		ChannelType:  req.ChannelType,
		DeletedToSeq: req.DeletedToSeq,
		UpdatedAt:    req.UpdatedAt,
	}
}

func cmdConversationStateToConversation(state CMDConversationState) ConversationState {
	return ConversationState{
		UID:          state.UID,
		Kind:         ConversationKindCMD,
		ChannelID:    state.ChannelID,
		ChannelType:  state.ChannelType,
		ReadSeq:      state.ReadSeq,
		DeletedToSeq: state.DeletedToSeq,
		ActiveAt:     state.ActiveAt,
		UpdatedAt:    state.UpdatedAt,
	}
}

func cmdConversationStateFromConversation(state ConversationState) CMDConversationState {
	return CMDConversationState{
		UID:          state.UID,
		ChannelID:    state.ChannelID,
		ChannelType:  state.ChannelType,
		ReadSeq:      state.ReadSeq,
		DeletedToSeq: state.DeletedToSeq,
		ActiveAt:     state.ActiveAt,
		UpdatedAt:    state.UpdatedAt,
	}
}

func cmdConversationStatesFromConversations(states []ConversationState) []CMDConversationState {
	out := make([]CMDConversationState, 0, len(states))
	for _, state := range states {
		out = append(out, cmdConversationStateFromConversation(state))
	}
	return out
}

func cmdConversationReadPatchToConversation(patch CMDConversationReadPatch) ConversationActivePatch {
	return ConversationActivePatch{
		UID:         patch.UID,
		Kind:        ConversationKindCMD,
		ChannelID:   patch.ChannelID,
		ChannelType: patch.ChannelType,
		ReadSeq:     patch.ReadSeq,
		UpdatedAt:   patch.UpdatedAt,
	}
}
