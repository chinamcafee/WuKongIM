package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// DiagnosticFinding identifies a physical row or a disk-joined group without
// exposing UIDs, channel names, device tokens, message payloads or raw keys.
// uint64 identifiers use JSON strings to avoid loss in JavaScript report tools.
type DiagnosticFinding struct {
	Code             string                    `json:"code"`
	NodeID           uint64                    `json:"node_id,string"`
	Table            string                    `json:"table,omitempty"`
	Shard            int                       `json:"shard"`
	RowID            uint64                    `json:"row_id,string,omitempty"`
	OwnerHash        uint64                    `json:"owner_hash,string,omitempty"`
	KeySHA256        string                    `json:"key_sha256,omitempty"`
	RelatedKeySHA256 string                    `json:"related_key_sha256,omitempty"`
	Count            uint64                    `json:"count,omitempty"`
	Detail           string                    `json:"detail,omitempty"`
	Plugin           *PluginDiagnosticEvidence `json:"plugin,omitempty"`
}

type DiagnosticCategory struct {
	Code     string              `json:"code"`
	Severity string              `json:"severity"`
	Impact   string              `json:"impact"`
	Count    uint64              `json:"findings"`
	ByNode   map[uint64]uint64   `json:"findings_by_node"`
	Samples  []DiagnosticFinding `json:"samples"`
}

type DiagnosticNode struct {
	NodeID     uint64            `json:"node_id,string"`
	Complete   bool              `json:"locked_capture_complete"`
	FileCount  uint64            `json:"file_count"`
	FileBytes  uint64            `json:"file_bytes"`
	DataDigest string            `json:"data_digest,omitempty"`
	Rows       uint64            `json:"rows_checked"`
	Tables     map[string]uint64 `json:"primary_rows_by_table"`
}

// DiagnosticReport is a bounded census of the implemented checks, not an
// authority selection, successful conversion or permission to start a target.
type DiagnosticReport struct {
	Version        int                  `json:"version"`
	PlanDigest     string               `json:"plan_digest"`
	Status         string               `json:"status"`
	ScanComplete   bool                 `json:"scan_complete"`
	CutoverReady   bool                 `json:"cutover_ready"`
	Nodes          []DiagnosticNode     `json:"nodes"`
	Checks         []string             `json:"checks"`
	NotCertified   []string             `json:"not_certified"`
	Categories     []DiagnosticCategory `json:"categories"`
	Findings       uint64               `json:"findings"`
	FindingsSHA256 string               `json:"findings_sha256"`
	FindingsFile   string               `json:"findings_file,omitempty"`
}

// CommandExitCode keeps the complete JSON report visible when blockers exist.
func (r DiagnosticReport) CommandExitCode() int {
	if r.Status != "no_findings_in_checked_scope" {
		return 1
	}
	return 0
}

type DiagnosticDecoder interface {
	RecordDecoder
	BusinessDecoder
	SourceIndexDecoder
}

type diagnostician struct {
	ctx        context.Context
	w          Workspace
	decoder    DiagnosticDecoder
	report     DiagnosticReport
	categories map[string]*DiagnosticCategory
	output     *json.Encoder
	batch      *captureBatch
}

// DiagnoseSources scans every readable stopped source and all supported row
// checks, collecting conflicts with disk-backed joins instead of stopping at
// the first business incompatibility. An unreadable node is explicitly marked
// incomplete; cancellation or workspace/output failure aborts immediately.
// Its workspace must have a diagnostic-only identity. No workflow seals or
// target records are produced, and every finding is streamed to details.
func DiagnoseSources(ctx context.Context, plan Plan, w Workspace, source Source, decoder DiagnosticDecoder, details io.Writer, progress func(uint64, string)) (DiagnosticReport, error) {
	if ctx == nil || w == nil || source == nil || decoder == nil || details == nil {
		return DiagnosticReport{}, errors.New("diagnose requires source, workspace, decoder and details output")
	}
	if len(plan.Sources) == 0 || len(plan.Sources) > 1024 {
		return DiagnosticReport{}, errors.New("diagnose requires 1..1024 source nodes")
	}
	ids := map[uint64]bool{}
	for _, node := range plan.Sources {
		if node.NodeID == 0 || ids[node.NodeID] {
			return DiagnosticReport{}, errors.New("duplicate or invalid diagnostic source node identity")
		}
		ids[node.NodeID] = true
	}
	h := sha256.New()
	d := &diagnostician{ctx: ctx, w: w, decoder: decoder, categories: map[string]*DiagnosticCategory{}, output: json.NewEncoder(io.MultiWriter(details, h)), batch: &captureBatch{ctx: ctx, workspace: w}}
	d.report = DiagnosticReport{Version: 2, PlanDigest: plan.Digest(), ScanComplete: true, Checks: []string{"locked source file inventories", "identity hints and unresolved references", "all operational source indexes and collisions", "native message fields and recovery budget", "message ID and idempotency uniqueness per node", "logical primary uniqueness per node", "retained sequence ranges and durable tails", "plugin bindings and conversation scalar compatibility", "subscriber history visibility and event cursor joins"}, NotCertified: []string{"successful authority selection and complete cross-replica business equivalence", "pending conversation recovery and API/SDK equivalence", "native target installation, recovery, cutover or performance acceptance"}}
	var capture SourceCapture
	for _, node := range plan.Sources {
		captureWorkspace := &diagnosticCaptureWorkspace{Workspace: w}
		c, err := captureSources(ctx, []NodeOptions{node}, source, captureWorkspace, progress, false)
		if captureWorkspace.err != nil {
			return d.report, captureWorkspace.err
		}
		info := DiagnosticNode{NodeID: node.NodeID, Tables: map[string]uint64{}}
		if err != nil {
			if ctx.Err() != nil {
				return d.report, ctx.Err()
			}
			d.report.ScanComplete = false
			if e := d.emit(DiagnosticFinding{Code: "source.capture_incomplete", NodeID: node.NodeID, Detail: diagnosticSHA([]byte(err.Error()))}); e != nil {
				return d.report, e
			}
		} else {
			n := c.Nodes[0]
			info.Complete = true
			info.FileCount = n.FileCount
			info.FileBytes = n.FileBytes
			info.DataDigest = n.DataDigest
			capture.Nodes = append(capture.Nodes, n)
		}
		d.report.Nodes = append(d.report.Nodes, info)
	}
	if err := validateCapturedAuthority(capture.Nodes); err != nil {
		if e := d.emit(DiagnosticFinding{Code: "source.authority_unresolved", Detail: diagnosticSHA([]byte(err.Error()))}); e != nil {
			return d.report, e
		}
	}
	if progress != nil {
		progress(0, "diagnostic identity census")
	}
	if err := d.catalog(capture); err != nil {
		return d.report, err
	}
	for i := range d.report.Nodes {
		info := &d.report.Nodes[i]
		if !info.Complete {
			continue
		}
		var shards int
		for _, n := range plan.Sources {
			if n.NodeID == info.NodeID {
				shards = n.ShardCount
			}
		}
		if progress != nil {
			progress(info.NodeID, "diagnostic business and operational index census")
		}
		err := walkSourceRows(ctx, w, info.NodeID, func(row Row) error {
			info.Rows++
			if row.Kind == Primary {
				info.Tables[row.Table]++
			}
			return d.row(info.NodeID, shards, row, plan.Exclusions)
		})
		if err != nil {
			return d.report, err
		}
	}
	if err := d.batch.flush(); err != nil {
		return d.report, err
	}
	if progress != nil {
		progress(0, "diagnostic full disk joins; no bounded-window deduplication")
	}
	if err := d.joins(); err != nil {
		return d.report, err
	}
	d.report.Status = "no_findings_in_checked_scope"
	for _, cat := range d.categories {
		d.report.Categories = append(d.report.Categories, *cat)
		if cat.Severity == "blocker" {
			d.report.Status = "blocked"
		}
	}
	if !d.report.ScanComplete {
		d.report.Status = "incomplete"
	}
	sort.Slice(d.report.Categories, func(i, j int) bool { return d.report.Categories[i].Code < d.report.Categories[j].Code })
	d.report.FindingsSHA256 = hex.EncodeToString(h.Sum(nil))
	return d.report, nil
}

// A source read failure leaves that node incomplete; scratch failures are fatal
// and must not be disguised as corrupt source data while other scans continue.
type diagnosticCaptureWorkspace struct {
	Workspace
	err error
}

func (w *diagnosticCaptureWorkspace) Put(ctx context.Context, rows []transfer.SpoolRow) error {
	err := w.Workspace.Put(ctx, rows)
	if err != nil {
		w.err = err
	}
	return err
}

func diagnosticSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func diagnosticRef(node uint64, row Row) DiagnosticFinding {
	return DiagnosticFinding{NodeID: node, Table: row.Table, Shard: row.Shard, RowID: row.ID, OwnerHash: row.Owner, KeySHA256: diagnosticSHA(sourceRowKey(node, row))}
}
func (d *diagnostician) issue(code string, node uint64, row Row) error {
	f := diagnosticRef(node, row)
	f.Code = code
	return d.emit(f)
}
func (d *diagnostician) emit(f DiagnosticFinding) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	cat := d.categories[f.Code]
	if cat == nil {
		severity, impact := diagnosticImpact(f.Code)
		cat = &DiagnosticCategory{Code: f.Code, Severity: severity, Impact: impact, ByNode: map[uint64]uint64{}}
		d.categories[f.Code] = cat
	}
	cat.Count++
	cat.ByNode[f.NodeID]++
	if len(cat.Samples) < 5 {
		cat.Samples = append(cat.Samples, f)
	}
	d.report.Findings++
	return d.output.Encode(f)
}
func (d *diagnostician) put(key string, value any) error {
	data, err := MarshalState(value)
	if err != nil {
		return err
	}
	return d.batch.add(transfer.SpoolRow{Key: []byte(key), Value: data})
}
func diagnosticImpact(code string) (string, string) {
	switch code {
	case "legacy.stream_storage_excluded":
		return "authorized_exclusion", "Only old Stream/StreamMeta storage is excluded; main Message rows remain required."
	case "management.archived":
		return "information", "Legacy management bytes are preserved in the source capture; business compatibility is checked separately."
	case "source.capture_incomplete":
		return "blocker", "This node was not fully readable or immutable; downstream counts omit its incomplete capture. Detail is the error SHA256, not private source text."
	case "source.authority_unresolved":
		return "blocker", "Persisted cluster authority validation failed; this is a status finding, not an exhaustive authority-error count. No replica is selected."
	case "duplicate.message_id":
		return "blocker", "Multiple physical messages share an ID within one source node; existing v3 uniqueness cannot preserve both without an explicit business decision."
	case "duplicate.idempotency":
		return "blocker", "Multiple messages share the v3 channel/sender/client key; retaining both conflicts with native idempotency."
	case "index.expected_collision":
		return "blocker", "One old operational lookup key has multiple required values. Count is collision groups, not duplicate pairs."
	case "message.sequence_gap", "message.retained_prefix", "message.tail_missing", "message.tail_mismatch":
		return "blocker", "The stored history is not a complete native v3 proposal sequence; no placeholder or new storage exception is generated."
	case "conversation.visibility":
		return "blocker", "A subscriber has channel history without a durable original conversation; visibility cannot be inferred safely."
	}
	return "blocker", "Existing source validation or native v3 conversion cannot preserve this record as-is. Resolve in the migration policy/tool; do not silently drop, merge, renumber or change v3 storage rules."
}
