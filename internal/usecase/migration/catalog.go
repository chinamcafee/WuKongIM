package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// SourceCatalog describes the complete disk-backed identity join. Counts are
// distinct identities across replicas, not the physical source row counts.
type SourceCatalog struct {
	Channels      uint64 `json:"channels"`
	UIDs          uint64 `json:"uids"`
	EventMessages uint64 `json:"event_messages"`
	Digest        string `json:"digest"`
}

// BuildSourceCatalog resolves identities before owner selection. Every source
// can supply an identity hint; no source can silently overwrite a conflicting
// hint. Records remain on disk, including high-fanout membership directories.
func BuildSourceCatalog(ctx context.Context, capture SourceCapture, workspace Workspace, decoder IdentityDecoder) (catalog SourceCatalog, err error) {
	if ctx == nil || workspace == nil || decoder == nil || capture.Digest == "" {
		return catalog, errors.New("source catalog requires a completed source capture")
	}
	batch := &captureBatch{ctx: ctx, workspace: workspace}
	for _, node := range capture.Nodes {
		err := walkSourceRows(ctx, workspace, node.NodeID, func(row Row) error {
			id, err := decoder.Identify(row)
			if err != nil {
				return fmt.Errorf("source node %d table %s: %w", node.NodeID, row.Table, err)
			}
			if id.UID != "" {
				if err := batch.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("catalog/uid/%016x", id.UIDHash)), Value: []byte(id.UID)}); err != nil {
					return err
				}
			}
			if id.UIDPersonalChannelHash != 0 {
				if id.UID == "" {
					return errors.New("personal channel hint is missing its source UID")
				}
				data, err := MarshalState(ChannelIdentity{ID: id.UID, Type: 1})
				if err != nil {
					return err
				}
				// Use the same collision-checked namespace as explicit channel
				// identities. No hint can overwrite a different original identity.
				if err := batch.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("catalog/channel/%016x", id.UIDPersonalChannelHash)), Value: data}); err != nil {
					return err
				}
			}
			if id.Channel.ID != "" {
				data, err := MarshalState(id.Channel)
				if err != nil {
					return err
				}
				if err := batch.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("catalog/channel/%016x", id.ChannelHash)), Value: data}); err != nil {
					return err
				}
				if err := batch.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("catalog/event-channel/%016x", id.EventChannelHash)), Value: data}); err != nil {
					return err
				}
				if id.ClientMsgNo != "" {
					if err := batch.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("catalog/event-message/%016x/%016x", id.EventChannelHash, id.ClientMsgHash)), Value: []byte(id.ClientMsgNo)}); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return catalog, err
		}
	}
	if err := batch.flush(); err != nil {
		return catalog, err
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	err = workspace.Walk(ctx, []byte("catalog/"), func(row transfer.SpoolRow) error {
		switch {
		case bytes.HasPrefix(row.Key, []byte("catalog/channel/")):
			catalog.Channels++
		case bytes.HasPrefix(row.Key, []byte("catalog/uid/")):
			catalog.UIDs++
		case bytes.HasPrefix(row.Key, []byte("catalog/event-message/")):
			catalog.EventMessages++
		}
		return enc.Encode(row)
	})
	if err != nil {
		return catalog, err
	}
	catalog.Digest = hex.EncodeToString(h.Sum(nil))
	return catalog, nil
}

func walkSourceRows(ctx context.Context, workspace Workspace, nodeID uint64, visit func(Row) error) error {
	prefix := []byte(fmt.Sprintf("source/%020d/rows/", nodeID))
	return workspace.Walk(ctx, prefix, func(record transfer.SpoolRow) error {
		var row Row
		if err := json.Unmarshal(record.Value, &row); err != nil {
			return err
		}
		expected := []byte(fmt.Sprintf("%s%04d/%x", prefix, row.Shard, row.Key))
		if !bytes.Equal(record.Key, expected) {
			return errors.New("captured source row identity mismatch")
		}
		return visit(row)
	})
}
