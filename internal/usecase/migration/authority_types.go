package migration

import "context"

// ChannelConfigEvidence retains migration roles without relaxing the strict
// import decoder. Identities are hashes; credentials and payloads are absent.
type ChannelConfigEvidence struct {
	Owner          uint64   `json:"owner_hash,string"`
	IdentitySHA256 string   `json:"identity_sha256"`
	RoutingHash    uint32   `json:"routing_hash"`
	Leader         uint64   `json:"leader,string"`
	Replicas       []uint64 `json:"replicas"`
	Learners       []uint64 `json:"learners"`
	ReplicaMax     uint16   `json:"replica_max"`
	Term           uint32   `json:"term"`
	Version        uint64   `json:"version,string"`
	MigrateFrom    uint64   `json:"migrate_from,string"`
	MigrateTo      uint64   `json:"migrate_to,string"`
	Status         uint8    `json:"status"`
	// SHA256 covers original config fields; the Slot log decoder substitutes
	// its log index for ConfVersion exactly as the original apply path does.
	SHA256 string `json:"sha256"`
	// NonMigrationSHA256 covers every original field except ConfVersion and
	// MigrateFrom/MigrateTo, including identities, membership and timestamps.
	NonMigrationSHA256 string `json:"non_migration_sha256"`
}

type ChannelConfigLog struct {
	Slot              uint32                `json:"slot"`
	Index             uint64                `json:"index,string"`
	Term              uint32                `json:"term"`
	Deleted           bool                  `json:"deleted"`
	Config            ChannelConfigEvidence `json:"config"`
	CommandSHA256     string                `json:"command_sha256"`
	DecodeErrorSHA256 string                `json:"decode_error_sha256,omitempty"`
	// EncodedVersion/SHA256 retain the command before the pinned apply path
	// substitutes log.Index, exposing historical version conventions explicitly.
	EncodedVersion      uint64 `json:"encoded_version,string"`
	EncodedConfigSHA256 string `json:"encoded_config_sha256,omitempty"`
}

// AuthoritySource exposes retained Slot config commands under the same source
// locks and two file inventories as ReadStoppedNode. Logs are not replayed.
type AuthoritySource interface {
	ReadAuthorityNode(context.Context, NodeOptions, func(Row) error, func(SourceFile) error, func(ChannelConfigLog) error) (NodeSnapshot, error)
}

// AuthorityCommandSource captures original config commands under the same
// source locks as business rows. Raw bytes stay in the private source archive,
// never the human-facing diagnostic. Unknown config commands must not disappear.
type AuthorityCommandSource interface {
	ReadAuthorityCommands(context.Context, NodeOptions, func(Row) error, func(SourceFile) error, func(RawConfigCommand) error) (NodeSnapshot, error)
}

// RawConfigCommand retains original Slot placement and exact command bytes.
// It is private archival evidence, never a decoded authority verdict.
type RawConfigCommand struct {
	Slot  uint32 `json:"slot"`
	Index uint64 `json:"index"`
	Term  uint32 `json:"term"`
	Data  []byte `json:"data"`
}

type AuthorityCommandDecoder interface {
	AuthorityDecoder
	DecodeAuthorityCommand(RawConfigCommand) ChannelConfigLog
}

type AuthorityDecoder interface {
	InspectChannelConfig(Row) (ChannelConfigEvidence, error)
	InspectMessage(Row, int) (MessageEvidence, error)
}

// MessageEvidence includes all stored message fields in SHA256, including
// original identity, sequence, term and protocol flags. Placement stays explicit.
type MessageEvidence struct {
	ID       uint64 `json:"message_id,string"`
	Sequence uint64 `json:"sequence,string"`
	Term     uint64 `json:"term,string"`
	SHA256   string `json:"sha256"`
	Invalid  bool   `json:"invalid,omitempty"`
}

type AuthorityNode struct {
	NodeID      uint64       `json:"node_id,string"`
	Complete    bool         `json:"complete"`
	ErrorSHA256 string       `json:"error_sha256,omitempty"`
	Snapshot    NodeSnapshot `json:"snapshot"`
}

// AuthorityReport is a diagnostic classification, never a source selection or
// permission to import. Per-channel and difference evidence is streamed to JSONL.
type AuthorityReport struct {
	Version              int               `json:"version"`
	Status               string            `json:"status"`
	Scope                string            `json:"scope"`
	ScanComplete         bool              `json:"scan_complete"`
	CutoverReady         bool              `json:"cutover_ready"`
	PlanDigest           string            `json:"plan_digest"`
	SourceCommit         string            `json:"source_commit"`
	Nodes                []AuthorityNode   `json:"nodes"`
	TopologyChecked      bool              `json:"topology_checked"`
	TopologyErrorSHA256  string            `json:"topology_error_sha256,omitempty"`
	Channels             uint64            `json:"channels"`
	Classes              map[string]uint64 `json:"classes"`
	ConfigDecodeFailures uint64            `json:"config_decode_failures"`
	DetailsSHA256        string            `json:"details_sha256"`
	DetailsFile          string            `json:"details_file,omitempty"`
	NotCertified         []string          `json:"not_certified"`
}

func (r AuthorityReport) CommandExitCode() int {
	if !r.ScanComplete || !r.TopologyChecked || r.ConfigDecodeFailures != 0 {
		return 1
	}
	for class, count := range r.Classes {
		if count != 0 && class != "consistent_formal_replicas" && class != "learner_lag_only" {
			return 1
		}
	}
	return 0
}

type AuthorityHistory struct {
	NodeID               uint64 `json:"node_id,string"`
	Messages             uint64 `json:"messages"`
	First                uint64 `json:"first,string"`
	Last                 uint64 `json:"last,string"`
	SHA256               string `json:"sha256"`
	Invalid              uint64 `json:"invalid"`
	DuplicateSequences   uint64 `json:"duplicate_sequences"`
	Gaps                 uint64 `json:"gaps"`
	MissingFromReference uint64 `json:"missing_from_reference"`
	OnlyOnNode           uint64 `json:"only_on_node"`
	Conflicts            uint64 `json:"conflicts"`
	TailRecords          uint64 `json:"tail_records"`
	DurableTail          uint64 `json:"durable_tail,string"`
}

type AuthorityConfigCopy struct {
	NodeID             uint64                `json:"node_id,string"`
	Config             ChannelConfigEvidence `json:"config"`
	RetainedConfigLogs uint64                `json:"retained_config_logs"`
	LastApplied        *ChannelConfigLog     `json:"last_applied,omitempty"`
	PreviousApplied    *ChannelConfigLog     `json:"previous_applied,omitempty"`
	// VersionRule identifies an exact original apply representation, never a
	// guessed timestamp or a rewritten source version.
	VersionRule      string `json:"version_rule,omitempty"`
	UnappliedChanges uint64 `json:"unapplied_changes"`
}

type AuthorityChannel struct {
	Type          string                `json:"type"`
	Owner         uint64                `json:"owner_hash,string"`
	Slot          uint32                `json:"slot"`
	Class         string                `json:"class"`
	Reasons       []string              `json:"reasons"`
	MigrationKind string                `json:"migration_kind"`
	ConfigCopies  []AuthorityConfigCopy `json:"config_copies"`
	ReferenceNode uint64                `json:"reference_node,string"`
	// CandidateNode is only a matching snapshot candidate. Prepare reconstructs
	// the proof from captured raw commands; this diagnostic creates no seal.
	CandidateNode uint64             `json:"candidate_node,string"`
	Histories     []AuthorityHistory `json:"histories"`
}
