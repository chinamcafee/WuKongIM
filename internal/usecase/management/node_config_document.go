package management

import (
	"context"
	"errors"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrNodeConfigDocumentUnsupported identifies a peer without TOML snapshot support.
var ErrNodeConfigDocumentUnsupported = errors.New("node does not support TOML config snapshots")

// MaxNodeConfigDocumentBytes bounds the redacted document and its metadata on the wire.
const MaxNodeConfigDocumentBytes = 1 << 20

// NodeConfigDocumentReader reads one selected node's already-redacted TOML.
type NodeConfigDocumentReader interface {
	NodeConfigDocument(context.Context, uint64) (NodeConfigDocument, error)
}

// NodeConfigDocument contains a startup snapshot, never an editable source file.
// TOML is encoded by the owning node; clients must not infer types from display strings.
type NodeConfigDocument struct {
	// GeneratedAt is captured during startup and remains stable across reads.
	GeneratedAt     time.Time
	NodeID          uint64
	Source          string
	RequiresRestart bool
	// TOML contains only encoded public values and fixed redaction comments.
	TOML     string
	Sections []NodeConfigDocumentSection
	Fields   []NodeConfigDocumentField
}

// NodeConfigDocumentSection locates a table header in the base document.
type NodeConfigDocumentSection struct {
	Path string
	Line int
}

// NodeConfigDocumentField supplies help and provenance without retaining hidden values.
// Line is one-based in TOML before optional localized help comments are inserted.
type NodeConfigDocumentField struct {
	Path          string
	EnvKey        string
	Label         string
	Description   string
	DescriptionZH string
	Source        string
	Line          int
	Redacted      bool
}

// Clone owns all mutable metadata while sharing immutable strings.
func (d NodeConfigDocument) Clone() NodeConfigDocument {
	d.Sections = append([]NodeConfigDocumentSection(nil), d.Sections...)
	d.Fields = append([]NodeConfigDocumentField(nil), d.Fields...)
	return d
}

// Validate rejects incomplete or oversized evidence before a caller displays it.
func (d NodeConfigDocument) Validate(nodeID uint64) error {
	if d.NodeID != nodeID || nodeID == 0 || d.Source != NodeConfigSnapshotSourceEffectiveStartup ||
		d.TOML == "" || len(d.TOML) > MaxNodeConfigDocumentBytes || len(d.Fields) == 0 ||
		len(d.Fields) > 1024 || len(d.Sections) > 128 {
		return ErrNodeConfigUnavailable
	}
	lines := strings.Count(d.TOML, "\n") + 1
	for _, field := range d.Fields {
		if field.Path == "" || field.Line < 1 || field.Line > lines {
			return ErrNodeConfigUnavailable
		}
	}
	for _, section := range d.Sections {
		if section.Path == "" || section.Line < 1 || section.Line > lines {
			return ErrNodeConfigUnavailable
		}
	}
	return nil
}

// NodeConfigDocument validates target membership before reading its startup document.
func (a *App) NodeConfigDocument(ctx context.Context, nodeID uint64) (NodeConfigDocument, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeConfigDocument{}, err
	}
	if nodeID == 0 {
		return NodeConfigDocument{}, metadb.ErrInvalidArgument
	}
	if a == nil {
		return NodeConfigDocument{}, ErrNodeConfigUnavailable
	}
	if err := a.ensureManagedNodeExists(ctx, nodeID); err != nil {
		return NodeConfigDocument{}, err
	}
	reader, ok := a.nodeConfig.(NodeConfigDocumentReader)
	if !ok {
		return NodeConfigDocument{}, ErrNodeConfigDocumentUnsupported
	}
	document, err := reader.NodeConfigDocument(ctx, nodeID)
	if err != nil {
		return NodeConfigDocument{}, err
	}
	if err := document.Validate(nodeID); err != nil {
		return NodeConfigDocument{}, err
	}
	return document, nil
}
