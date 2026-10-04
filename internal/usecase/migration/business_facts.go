package migration

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"time"
)

// BusinessFacts contains decoded values from one authoritative original row.
// Target placement and policy joins remain the migration use case's job.
type BusinessFacts struct {
	// CMDMessage follows the pinned source routing and SyncOnce semantics.
	CMDMessage    bool
	Member        *SourceMember
	Message       *channelcompat.Message
	Conversation  *SourceConversation
	User          *SourceUser
	Channel       *SourceChannel
	Device        *SourceDevice
	EventState    *meta.MessageEventState
	EventCursor   *meta.MessageEventCursor
	Tail          *SourceMessageTail
	PluginBinding *SourcePluginBinding
	Plugin        *SourcePlugin
}

// SourcePlugin retains original node-local configuration and registration.
// Registration is not a replacement for the executable or runtime proof.
type SourcePlugin struct {
	No, Name, Version      string
	Methods                []string
	Config, ConfigTemplate []byte
	Status, Priority       uint32
	CreatedAt, UpdatedAt   *time.Time
}

// SourcePluginBinding is the original runtime binding, independent of the
// obsolete User.PluginNo field. Original nanosecond timestamps remain exact.
type SourcePluginBinding struct {
	SourceID    uint64 `json:"source_id"`
	UID         string `json:"uid"`
	PluginNo    string `json:"plugin_no"`
	CreatedAtNS int64  `json:"created_at_ns"`
	UpdatedAtNS int64  `json:"updated_at_ns"`
}

// SourceMessageTail retains the original append boundary and its wall-clock
// timestamp. The latter is not the message's protocol timestamp.
type SourceMessageTail struct {
	Channel      ChannelIdentity `json:"channel"`
	LastSeq      uint64          `json:"last_seq"`
	LastAppendNS int64           `json:"last_append_ns"`
}

// SourceDevice retains old primary identity and nanosecond timestamps even
// though v3 authentication addresses a device by UID and device flag.
type SourceDevice struct {
	SourceID    uint64 `json:"source_id"`
	UID         string `json:"uid"`
	Token       string `json:"token"`
	Flag        uint64 `json:"flag"`
	Level       uint8  `json:"level"`
	CreatedAtNS int64  `json:"created_at_ns"`
	UpdatedAtNS int64  `json:"updated_at_ns"`
}

// SourceUser retains account existence, original identity and legacy binding.
// Historical counters remain in the source archive; they are not live sessions.
type SourceUser struct {
	SourceID    uint64 `json:"source_id"`
	UID         string `json:"uid"`
	PluginNo    string `json:"plugin_no"`
	CreatedAtNS int64  `json:"created_at_ns"`
	UpdatedAtNS int64  `json:"updated_at_ns"`
}

// SourceChannel contains persisted policy and old informational counters.
// The original server counts actual member rows; these stale columns never
// establish membership completeness or allocate v3 state.
type SourceChannel struct {
	ChannelIdentity
	SourceID        uint64 `json:"source_id"`
	Ban             bool   `json:"ban"`
	Disband         bool   `json:"disband"`
	SendBan         bool   `json:"send_ban"`
	AllowStranger   bool   `json:"allow_stranger"`
	Large           bool   `json:"large"`
	SubscriberCount uint32 `json:"subscriber_count"`
	AllowlistCount  uint32 `json:"allowlist_count"`
	DenylistCount   uint32 `json:"denylist_count"`
	CreatedAtNS     int64  `json:"created_at_ns"`
	UpdatedAtNS     int64  `json:"updated_at_ns"`
}

// SourceConversation preserves read/delete position independently of member
// presence. Pending intents have no original row ID or durable creation time.
type SourceConversation struct {
	SourceID     uint64          `json:"source_id"`
	UID          string          `json:"uid"`
	Channel      ChannelIdentity `json:"channel"`
	Type         uint8           `json:"type"`
	ReadSeq      uint64          `json:"read_seq"`
	DeletedToSeq uint64          `json:"deleted_to_seq"`
	UnreadCount  uint32          `json:"unread_count"`
	CreatedAtNS  int64           `json:"created_at_ns"`
	UpdatedAtNS  int64           `json:"updated_at_ns"`
	Pending      bool            `json:"pending"`
}

// SourceMember describes one ordinary or permission-list member after the
// owning channel hash has been resolved against the complete identity catalog.
type SourceMember struct {
	SourceID    uint64          `json:"source_id"`
	UID         string          `json:"uid"`
	Channel     ChannelIdentity `json:"channel"`
	CreatedAtNS int64           `json:"created_at_ns"`
	UpdatedAtNS int64           `json:"updated_at_ns"`
}
