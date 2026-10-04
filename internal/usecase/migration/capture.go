package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// Workspace supplies a bounded durable sort/join workspace. The composition
// root opens it with the complete immutable migration-plan identity.
type Workspace interface {
	Put(context.Context, []transfer.SpoolRow) error
	Get(context.Context, []byte) ([]byte, bool, error)
	Walk(context.Context, []byte, func(transfer.SpoolRow) error) error
}

// SourceCapture is source evidence only. Completion of this phase does not
// certify business conversion, target replication, API behavior or cutover.
type SourceCapture struct {
	Nodes  []NodeSnapshot    `json:"nodes"`
	Tables map[string]uint64 `json:"primary_rows_by_table"`
	Digest string            `json:"digest"`
	// Authority binds original retained config commands and source shard counts.
	// Absent evidence keeps the older strict transition rejection behavior.
	Authority            []CapturedAuthority `json:"authority,omitempty"`
	MarkedConfigurations uint64              `json:"marked_configurations,omitempty"`
}

// CapturedAuthority binds one node's original shard layout and ordered config
// command stream to the capture and portable archive manifest.
type CapturedAuthority struct {
	NodeID     uint64 `json:"node_id"`
	ShardCount int    `json:"shard_count"`
	Commands   uint64 `json:"commands"`
	SHA256     string `json:"sha256"`
}

// CaptureSources preserves all source rows and file identities before selecting
// authorities. Each immutable node inventory is sealed before its first row is
// ingested, so interruption cannot silently adopt a changed or partial source.
func CaptureSources(ctx context.Context, nodes []NodeOptions, source Source, workspace Workspace, progress func(uint64, string)) (capture SourceCapture, err error) {
	return captureSources(ctx, nodes, source, workspace, progress, true)
}

// captureSources can defer the cross-node check for diagnostics, which capture
// each node independently and explicitly report incomplete source inventories.
func captureSources(ctx context.Context, nodes []NodeOptions, source Source, workspace Workspace, progress func(uint64, string), checkAuthority bool) (capture SourceCapture, err error) {
	if ctx == nil || source == nil || workspace == nil || len(nodes) == 0 || len(nodes) > 1024 {
		return capture, errors.New("migration requires 1..1024 complete source nodes")
	}
	nodes = append([]NodeOptions(nil), nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	ids := map[uint64]bool{}
	paths := map[string]bool{}
	for _, n := range nodes {
		root, err := filepath.EvalSymlinks(n.DataDir)
		if err != nil {
			return capture, err
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return capture, err
		}
		if n.NodeID == 0 || ids[n.NodeID] || paths[root] {
			return capture, errors.New("duplicate or invalid source node identity/directory")
		}
		ids[n.NodeID], paths[root] = true, true
	}
	capture.Tables = map[string]uint64{}
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return capture, err
		}
		if progress != nil {
			progress(node.NodeID, "reading stopped source")
		}
		prefix := fmt.Sprintf("source/%020d/", node.NodeID)
		filesHash := sha256.New()
		filesEncoder := json.NewEncoder(filesHash)
		batch := &captureBatch{ctx: ctx, workspace: workspace}
		sealed := false
		seal := func() error {
			if sealed {
				return nil
			}
			if err := batch.flush(); err != nil {
				return err
			}
			if err := workspace.Put(ctx, []transfer.SpoolRow{{Key: []byte(prefix + "inventory"), Value: []byte(hex.EncodeToString(filesHash.Sum(nil)))}}); err != nil {
				return fmt.Errorf("source %d inventory conflict: %w", node.NodeID, err)
			}
			sealed = true
			return nil
		}
		read := source.ReadStoppedNode
		var authority *CapturedAuthority
		if original, ok := source.(AuthorityCommandSource); ok {
			authority = &CapturedAuthority{NodeID: node.NodeID, ShardCount: node.ShardCount}
			read = func(ctx context.Context, n NodeOptions, rows func(Row) error, files func(SourceFile) error) (NodeSnapshot, error) {
				return original.ReadAuthorityCommands(ctx, n, rows, files, func(command RawConfigCommand) error {
					if err := seal(); err != nil {
						return err
					}
					data, err := json.Marshal(command)
					if err != nil {
						return err
					}
					return batch.add(transfer.SpoolRow{Key: capturedCommandKey(n.NodeID, command), Value: data})
				})
			}
		}
		snapshot, err := read(ctx, node, func(row Row) error {
			if err := seal(); err != nil {
				return err
			}
			value, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if row.Kind == Primary {
				capture.Tables[row.Table]++
			}
			if markedChannelConfig(row) {
				capture.MarkedConfigurations++
			}
			key := []byte(fmt.Sprintf("%srows/%04d/%x", prefix, row.Shard, row.Key))
			return batch.add(transfer.SpoolRow{Key: key, Value: value})
		}, func(file SourceFile) error {
			if sealed {
				return errors.New("source file inventory arrived after rows")
			}
			if err := filesEncoder.Encode(file); err != nil {
				return err
			}
			value, err := json.Marshal(file)
			if err != nil {
				return err
			}
			return batch.add(transfer.SpoolRow{Key: []byte(prefix + "files/" + file.Path), Value: value})
		})
		if err != nil {
			return capture, fmt.Errorf("source node %d: %w", node.NodeID, err)
		}
		if err := seal(); err != nil {
			return capture, err
		}
		if snapshot.DataDigest != hex.EncodeToString(filesHash.Sum(nil)) {
			return capture, errors.New("source snapshot differs from its streamed file inventory")
		}
		if err := batch.flush(); err != nil {
			return capture, err
		}
		if authority != nil {
			authority.Commands, authority.SHA256, err = walkCapturedCommands(ctx, workspace, node.NodeID, nil)
			if err != nil {
				return capture, err
			}
			capture.Authority = append(capture.Authority, *authority)
		}
		value, err := json.Marshal(snapshot)
		if err != nil {
			return capture, err
		}
		if err := workspace.Put(ctx, []transfer.SpoolRow{{Key: []byte(prefix + "snapshot"), Value: value}}); err != nil {
			return capture, fmt.Errorf("source %d snapshot conflict: %w", node.NodeID, err)
		}
		capture.Nodes = append(capture.Nodes, snapshot)
	}
	if checkAuthority {
		if err := validateCapturedAuthority(capture.Nodes); err != nil {
			return capture, err
		}
	}
	digest, err := json.Marshal(capture)
	if err != nil {
		return capture, err
	}
	sum := sha256.Sum256(digest)
	capture.Digest = hex.EncodeToString(sum[:])
	if progress != nil && checkAuthority {
		progress(0, "source inventory and persisted authority checked")
	}
	return capture, nil
}

// captureBatch bounds both count and encoded bytes. A single large original
// row may exceed the ordinary 8 MiB batch, but never the 128 MiB record ceiling.
type captureBatch struct {
	ctx       context.Context
	workspace Workspace
	rows      []transfer.SpoolRow
	bytes     int
}

func (b *captureBatch) add(row transfer.SpoolRow) error {
	size := len(row.Key) + len(row.Value)
	if size > 128<<20 {
		return errors.New("encoded migration record exceeds 128 MiB")
	}
	if len(b.rows) > 0 && (len(b.rows) >= 1024 || size > (8<<20)-b.bytes) {
		if err := b.flush(); err != nil {
			return err
		}
	}
	b.rows = append(b.rows, row)
	b.bytes += size
	if b.bytes >= 8<<20 {
		return b.flush()
	}
	return nil
}
func (b *captureBatch) flush() error {
	if len(b.rows) == 0 {
		return nil
	}
	if err := b.workspace.Put(b.ctx, b.rows); err != nil {
		return err
	}
	b.rows = nil
	b.bytes = 0
	return nil
}

func validateCapturedAuthority(nodes []NodeSnapshot) error {
	if len(nodes) == 0 {
		return errors.New("missing source snapshots")
	}
	byID := make(map[uint64]NodeSnapshot, len(nodes))
	var topology []byte
	for _, node := range nodes {
		canonical := node.Config
		canonical.Nodes = append([]SourceNode(nil), canonical.Nodes...)
		for i := range canonical.Nodes {
			// Original updateNodeOnlineStatus derives these display statistics
			// per process (including time.Now), not from replicated command bytes.
			// Keep the original snapshot intact and compare all authority fields.
			canonical.Nodes[i].OfflineCount = 0
			canonical.Nodes[i].LastOffline = 0
		}
		sort.Slice(canonical.Nodes, func(i, j int) bool { return canonical.Nodes[i].ID < canonical.Nodes[j].ID })
		canonical.Slots = append([]SourceSlot(nil), canonical.Slots...)
		sort.Slice(canonical.Slots, func(i, j int) bool { return canonical.Slots[i].ID < canonical.Slots[j].ID })
		for i := range canonical.Slots {
			canonical.Slots[i].Replicas = append([]uint64(nil), canonical.Slots[i].Replicas...)
			sort.Slice(canonical.Slots[i].Replicas, func(a, b int) bool { return canonical.Slots[i].Replicas[a] < canonical.Slots[i].Replicas[b] })
		}
		data, err := json.Marshal(canonical)
		if err != nil {
			return err
		}
		if topology == nil {
			topology = data
		} else if !bytes.Equal(topology, data) {
			return fmt.Errorf("source node %d disagrees on persisted cluster configuration", node.NodeID)
		}
		if _, exists := byID[node.NodeID]; exists {
			return errors.New("duplicate source snapshot identity")
		}
		byID[node.NodeID] = node
		if node.ConfigProgress.LastIndex != node.ConfigProgress.AppliedIndex || node.ConfigProgress.AppliedIndex != node.Config.Version {
			return fmt.Errorf("source node %d has unapplied or inconsistent configuration logs", node.NodeID)
		}
		if node.NotificationDepth != 0 {
			return fmt.Errorf("source node %d retains %d notification queue entries; do not replay automatically", node.NodeID, node.NotificationDepth)
		}
	}
	if len(nodes) != len(nodes[0].Config.Nodes) {
		return errors.New("source inventory is incomplete: every configured v2 node is required")
	}
	for _, expected := range nodes[0].Config.Nodes {
		if _, ok := byID[expected.ID]; !ok {
			return fmt.Errorf("source node %d is missing", expected.ID)
		}
	}
	for _, slot := range nodes[0].Config.Slots {
		leader := byID[slot.Leader]
		head, err := capturedSlotProgress(leader, slot.ID)
		if err != nil {
			return err
		}
		if head.LastIndex != head.AppliedIndex {
			return fmt.Errorf("source Slot %d leader has an unapplied log tail", slot.ID)
		}
		for _, id := range slot.Replicas {
			replica, ok := byID[id]
			if !ok {
				return fmt.Errorf("source Slot %d replica node %d is missing", slot.ID, id)
			}
			tail, err := capturedSlotProgress(replica, slot.ID)
			if err != nil {
				return err
			}
			if tail.AppliedIndex != head.AppliedIndex || tail.LastIndex != head.LastIndex || tail.LastTerm != head.LastTerm || tail.LastDigest != head.LastDigest || (tail.FirstIndex == head.FirstIndex && tail.LogDigest != head.LogDigest) {
				return fmt.Errorf("source Slot %d replica node %d has divergent or unconverged persisted logs", slot.ID, id)
			}
		}
	}
	return nil
}
func capturedSlotProgress(node NodeSnapshot, slot uint32) (LogProgress, error) {
	if int(slot) >= len(node.SlotProgress) {
		return LogProgress{}, fmt.Errorf("source node %d lacks Slot %d progress", node.NodeID, slot)
	}
	p := node.SlotProgress[slot]
	if p.Group != strconv.FormatUint(uint64(slot), 10) {
		return LogProgress{}, errors.New("source Slot progress inventory is not canonical")
	}
	return p, nil
}
