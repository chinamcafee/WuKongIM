package migration

// DedupeMessage is a payload-free, source-bound message identity. ClientKeySHA256
// covers the channel, sender and nonempty ClientMsgNo tuple; an empty key opts
// out exactly as native v3 does when either sender or ClientMsgNo is empty.
type DedupeMessage struct {
	// UnsupportedFields names protocol fields the native read/recovery contract
	// cannot preserve. It contains field names only, never their source values.
	UnsupportedFields []string `json:"unsupported_fields,omitempty"`
	CMD               bool     `json:"cmd,omitempty"`
	// Stream includes Setting stream messages as well as legacy StreamNo parents.
	Stream          bool   `json:"stream,omitempty"`
	NodeID          uint64 `json:"node_id,string"`
	Owner           uint64 `json:"owner_hash,string"`
	ChannelSHA256   string `json:"channel_sha256"`
	ClientKeySHA256 string `json:"client_key_sha256,omitempty"`
	StreamParent    bool   `json:"stream_parent,omitempty"`
	MessageEvidence
}

type DedupeDecoder interface {
	InspectDedupeMessage(Row, int) (DedupeMessage, error)
}

// DedupeGroup records the greatest original sequence for a duplicate identity.
// Cross-channel MessageIDs have no comparable sequence and remain unresolved.
type DedupeGroup struct {
	Type      string        `json:"type"`
	Key       string        `json:"key"`
	Kind      string        `json:"kind"`
	Count     uint64        `json:"count"`
	Ambiguous bool          `json:"ambiguous"`
	Latest    DedupeMessage `json:"latest"`
}

// DedupeDrop is a candidate omission from target output only. Every cited
// winner must itself survive both identity rules before the plan is resolved.
type DedupeDrop struct {
	Type       string          `json:"type"`
	Message    DedupeMessage   `json:"message"`
	Winners    []DedupeMessage `json:"winners"`
	Reasons    []string        `json:"reasons"`
	Unresolved bool            `json:"unresolved"`
}

type DedupeChannel struct {
	CMDDrops             uint64 `json:"candidate_cmd_drops"`
	StreamDrops          uint64 `json:"candidate_stream_drops"`
	Type                 string `json:"type"`
	NodeID               uint64 `json:"node_id,string"`
	Owner                uint64 `json:"owner_hash,string"`
	Messages             uint64 `json:"messages"`
	Dropped              uint64 `json:"candidate_drops"`
	Retained             uint64 `json:"candidate_retained"`
	LastSequence         uint64 `json:"last_sequence,string"`
	FirstDrop            uint64 `json:"first_drop,string"`
	ChangedSequences     uint64 `json:"survivors_requiring_renumbering"`
	SourceGaps           uint64 `json:"source_gaps"`
	StreamParents        uint64 `json:"stream_parents"`
	DroppedStreamParents uint64 `json:"candidate_dropped_stream_parents"`
}

// DedupeProtocolImpact separates retained-field blockers from rows omitted by
// the requested policy. Counts are physical rows per node, not unique replicas.
type DedupeProtocolImpact struct {
	Retained            uint64            `json:"candidate_retained"`
	RetainedUnsupported uint64            `json:"candidate_retained_with_unsupported_fields"`
	RetainedFields      map[string]uint64 `json:"retained_fields"`
	OmittedUnsupported  uint64            `json:"candidate_omitted_with_unsupported_fields"`
	OmittedFields       map[string]uint64 `json:"omitted_fields"`
	// Samples keeps at most three payload-free retained examples per field.
	Samples map[string][]DedupeMessage `json:"retained_samples"`
}

type DedupeNode struct {
	Protocol             DedupeProtocolImpact `json:"protocol_impact"`
	CMDDrops             uint64               `json:"candidate_cmd_drops"`
	StreamDrops          uint64               `json:"candidate_stream_drops"`
	NodeID               uint64               `json:"node_id,string"`
	Snapshot             NodeSnapshot         `json:"snapshot"`
	Messages             uint64               `json:"messages"`
	MessageIDGroups      uint64               `json:"message_id_groups"`
	ClientKeyGroups      uint64               `json:"client_key_groups"`
	Dropped              uint64               `json:"candidate_drops"`
	AffectedChannels     uint64               `json:"affected_channels"`
	ChangedSequences     uint64               `json:"survivors_requiring_renumbering"`
	StreamParents        uint64               `json:"stream_parents"`
	DroppedStreamParents uint64               `json:"candidate_dropped_stream_parents"`
}

// DedupeReport is an impact plan, not a source-authority or conversion seal.
// It never authorizes sequence renumbering or changes the prepare/import path.
type DedupeReport struct {
	Version             int          `json:"version"`
	Status              string       `json:"status"`
	ScanComplete        bool         `json:"scan_complete"`
	CutoverReady        bool         `json:"cutover_ready"`
	PlanDigest          string       `json:"plan_digest"`
	SourceCommit        string       `json:"source_commit"`
	Rule                string       `json:"rule"`
	Nodes               []DedupeNode `json:"nodes"`
	Unresolved          uint64       `json:"unresolved"`
	RenumberingRequired bool         `json:"renumbering_required"`
	DetailsSHA256       string       `json:"details_sha256"`
	DetailsFile         string       `json:"details_file,omitempty"`
	NotCertified        []string     `json:"not_certified"`
}

func (r DedupeReport) CommandExitCode() int {
	if !r.ScanComplete || r.Unresolved != 0 {
		return 1
	}
	return 0
}
