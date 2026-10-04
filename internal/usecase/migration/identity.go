package migration

// ChannelIdentity is a lossless logical identifier, independent of both the
// source's physical hashes and the target's hash-Slot placement.
type ChannelIdentity struct {
	ID   string `json:"id"`
	Type uint8  `json:"type"`
}

// RecordIdentity exposes independently checked identity hints from an original
// row. Hash-only references must be resolved through the complete disk catalog;
// a hash collision or missing identity must never select an arbitrary owner.
type RecordIdentity struct {
	// UIDPersonalChannelHash identifies the original personal-permission
	// namespace (UID, type 1) proved by an account/device row. It is an identity
	// hint only and never materializes a target channel or grants membership.
	UIDPersonalChannelHash uint64          `json:"uid_personal_channel_hash,omitempty"`
	UID                    string          `json:"uid,omitempty"`
	UIDHash                uint64          `json:"uid_hash,omitempty"`
	Channel                ChannelIdentity `json:"channel"`
	ChannelHash            uint64          `json:"channel_hash,omitempty"`
	EventChannelHash       uint64          `json:"event_channel_hash,omitempty"`
	ClientMsgNo            string          `json:"client_msg_no,omitempty"`
	ClientMsgHash          uint64          `json:"client_msg_hash,omitempty"`
}

// IdentityDecoder validates source key/value identities without interpreting
// which replica or recovered business value the migration should select.
type IdentityDecoder interface {
	Identify(Row) (RecordIdentity, error)
}
