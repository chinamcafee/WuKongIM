package migration

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

type authorityAudit struct {
	ctx      context.Context
	w        Workspace
	b        *captureBatch
	out      *json.Encoder
	report   AuthorityReport
	nodes    map[uint64]NodeSnapshot
	badSlots map[uint32]bool
}

type configCopy struct {
	NodeID  uint64                `json:"node_id"`
	Config  ChannelConfigEvidence `json:"config"`
	Invalid bool                  `json:"invalid"`
}

type messageCopy struct {
	NodeID uint64 `json:"node_id,string"`
	Shard  int    `json:"shard"`
	MessageEvidence
}

type authorityTail struct {
	Value   []byte `json:"value"`
	Invalid bool   `json:"invalid"`
}

// AuditSourceAuthority inventories only current migration-marked channels.
// Both locked passes must bind to the same files. Configs/logs are disk sorted;
// the second pass writes only selected message digests, never payload copies.
// It deliberately does not select, repair, truncate or normalize source data.
func AuditSourceAuthority(ctx context.Context, plan Plan, w Workspace, source AuthoritySource, decoder AuthorityDecoder, details io.Writer, progress func(uint64, string)) (AuthorityReport, error) {
	return auditSourceAuthority(ctx, plan, w, source, decoder, details, progress, nil)
}

func auditSourceAuthority(ctx context.Context, plan Plan, w Workspace, source AuthoritySource, decoder AuthorityDecoder, details io.Writer, progress func(uint64, string), visit func(AuthorityChannel) error) (AuthorityReport, error) {
	if ctx == nil || w == nil || source == nil || decoder == nil || details == nil || len(plan.Sources) == 0 || len(plan.Sources) > 1024 {
		return AuthorityReport{}, errors.New("authority audit requires source inventory, workspace and details writer")
	}
	h := sha256.New()
	a := &authorityAudit{ctx: ctx, w: w, b: &captureBatch{ctx: ctx, workspace: w}, out: json.NewEncoder(io.MultiWriter(details, h)), nodes: map[uint64]NodeSnapshot{}, badSlots: map[uint32]bool{}}
	a.report = AuthorityReport{Version: 3, Status: "classified", Scope: "current ChannelClusterConfig rows with nonzero MigrateFrom or MigrateTo", ScanComplete: true, PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit, Classes: map[string]uint64{}, NotCertified: []string{"historical client ACK or independently persisted channel commit watermark", "unmarked channels, business metadata equivalence and message-event projections", "successful prepare, source selection, target installation, API behavior or cutover"}}
	opts := append([]NodeOptions(nil), plan.Sources...)
	sort.Slice(opts, func(i, j int) bool { return opts[i].NodeID < opts[j].NodeID })
	seen := map[uint64]bool{}
	for _, n := range opts {
		if n.NodeID == 0 || seen[n.NodeID] {
			return a.report, errors.New("duplicate or zero source node")
		}
		seen[n.NodeID] = true
		if progress != nil {
			progress(n.NodeID, "authority: reading configurations and retained Slot commands")
		}
		files := sha256.New()
		enc := json.NewEncoder(files)
		sealed := false
		seal := func() error {
			if sealed {
				return nil
			}
			sealed = true
			return w.Put(ctx, []transfer.SpoolRow{{Key: []byte(fmt.Sprintf("authority/source/%020d/inventory", n.NodeID)), Value: []byte(hex.EncodeToString(files.Sum(nil)))}})
		}
		snapshot, err := source.ReadAuthorityNode(ctx, n, func(row Row) error {
			if err := seal(); err != nil {
				return err
			}
			if row.Table != "ChannelClusterConfig" || row.Kind != Primary {
				return nil
			}
			marked := false
			for _, name := range []string{"MigrateFrom", "MigrateTo"} {
				for _, v := range row.Fields[name] {
					marked = marked || v != 0
				}
			}
			if marked {
				if err := a.put(fmt.Sprintf("authority/scope/%020d", row.ID), true); err != nil {
					return err
				}
			}
			c, err := decoder.InspectChannelConfig(row)
			copy := configCopy{NodeID: n.NodeID, Config: c, Invalid: err != nil}
			if err != nil {
				a.report.ConfigDecodeFailures++
				if err := a.out.Encode(map[string]any{"type": "config_decode_error", "node_id": fmt.Sprint(n.NodeID), "owner_hash": fmt.Sprint(row.ID), "error_sha256": diagnosticSHA([]byte(err.Error()))}); err != nil {
					return err
				}
			}
			return a.put(fmt.Sprintf("authority/config/%020d/%020d/%04d", row.ID, n.NodeID, row.Shard), copy)
		}, func(f SourceFile) error {
			if sealed {
				return errors.New("authority file inventory after rows")
			}
			return enc.Encode(f)
		}, func(e ChannelConfigLog) error {
			if err := seal(); err != nil {
				return err
			}
			if e.DecodeErrorSHA256 != "" {
				a.report.ConfigDecodeFailures++
				a.badSlots[e.Slot] = true
				return a.out.Encode(map[string]any{"type": "config_log_decode_error", "node_id": fmt.Sprint(n.NodeID), "log": e})
			}
			return a.put(fmt.Sprintf("authority/log/%020d/%020d/%010d/%020d", e.Config.Owner, n.NodeID, e.Slot, e.Index), e)
		})
		if err == nil {
			err = seal()
		}
		if err == nil && snapshot.DataDigest != hex.EncodeToString(files.Sum(nil)) {
			err = errors.New("authority snapshot file digest disagreement")
		}
		if e := a.b.flush(); e != nil {
			return a.report, e
		}
		node := AuthorityNode{NodeID: n.NodeID, Complete: err == nil, Snapshot: snapshot}
		if err != nil {
			node.ErrorSHA256 = diagnosticSHA([]byte(err.Error()))
			a.report.ScanComplete = false
			if ctx.Err() != nil {
				return a.report, ctx.Err()
			}
		} else {
			a.nodes[n.NodeID] = snapshot
		}
		a.report.Nodes = append(a.report.Nodes, node)
	}
	// The second locked pass compares every file digest to the first before
	// accepting its targeted rows. A failed node never contributes to a decision.
	for i, n := range opts {
		previous, ok := a.nodes[n.NodeID]
		if !ok {
			continue
		}
		if progress != nil {
			progress(n.NodeID, "authority: hashing marked-channel histories")
		}
		snapshot, err := source.ReadAuthorityNode(ctx, n, func(row Row) error {
			if row.Table != "Message" || (row.Kind != Primary && row.Kind != Other) {
				return nil
			}
			owner := row.Owner
			if row.Kind == Other {
				if len(row.Key) < 12 {
					return errors.New("invalid message tail key")
				}
				owner = binary.BigEndian.Uint64(row.Key[4:12])
			}
			_, marked, err := w.Get(ctx, []byte(fmt.Sprintf("authority/scope/%020d", owner)))
			if err != nil || !marked {
				return err
			}
			if row.Kind == Other {
				invalid := len(row.Key) != 12 || len(row.Value) != 16 || n.ShardCount < 1
				if n.ShardCount > 0 {
					invalid = invalid || row.Shard != int(owner%uint64(n.ShardCount))
				}
				return a.put(fmt.Sprintf("authority/tail/%020d/%020d/%04d/%x", owner, n.NodeID, row.Shard, row.Key), authorityTail{Value: row.Value, Invalid: invalid})
			}
			m, err := decoder.InspectMessage(row, n.ShardCount)
			if err != nil {
				data, e := json.Marshal(row.Fields)
				if e != nil {
					return e
				}
				m = MessageEvidence{Sequence: row.ID, SHA256: diagnosticSHA(data), Invalid: true}
			}
			return a.put(fmt.Sprintf("authority/message/%020d/%020d/%020d/%04d", owner, row.ID, n.NodeID, row.Shard), messageCopy{NodeID: n.NodeID, Shard: row.Shard, MessageEvidence: m})
		}, nil, func(ChannelConfigLog) error { return nil })
		if e := a.b.flush(); e != nil {
			return a.report, e
		}
		if err == nil && snapshot.DataDigest != previous.DataDigest {
			err = errors.New("source changed between authority audit passes")
		}
		if err != nil {
			a.report.ScanComplete = false
			a.report.Nodes[i].Complete = false
			a.report.Nodes[i].ErrorSHA256 = diagnosticSHA([]byte(err.Error()))
			delete(a.nodes, n.NodeID)
			if ctx.Err() != nil {
				return a.report, ctx.Err()
			}
		}
	}
	var snapshots []NodeSnapshot
	for _, n := range a.report.Nodes {
		if n.Complete {
			snapshots = append(snapshots, n.Snapshot)
		}
	}
	if err := validateCapturedAuthority(snapshots); err != nil {
		a.report.TopologyErrorSHA256 = diagnosticSHA([]byte(err.Error()))
	} else {
		a.report.TopologyChecked = a.report.ScanComplete
	}
	if progress != nil {
		progress(0, "authority: comparing marked-channel configurations and messages")
	}
	err := w.Walk(ctx, []byte("authority/scope/"), func(row transfer.SpoolRow) error {
		var owner uint64
		if _, err := fmt.Sscanf(string(row.Key), "authority/scope/%d", &owner); err != nil {
			return err
		}
		c, err := a.channel(owner)
		if err != nil {
			return err
		}
		a.report.Channels++
		a.report.Classes[c.Class]++
		if visit != nil {
			if err := visit(c); err != nil {
				return err
			}
		}
		return a.out.Encode(c)
	})
	if err != nil {
		return a.report, err
	}
	a.report.DetailsSHA256 = hex.EncodeToString(h.Sum(nil))
	if !a.report.ScanComplete {
		a.report.Status = "incomplete"
	} else if a.report.CommandExitCode() != 0 {
		a.report.Status = "unresolved"
	}
	return a.report, nil
}

func (a *authorityAudit) put(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return a.b.add(transfer.SpoolRow{Key: []byte(key), Value: data})
}

func (a *authorityAudit) walk(prefix string, visit func([]byte) error) error {
	return a.w.Walk(a.ctx, []byte(prefix), func(row transfer.SpoolRow) error { return visit(row.Value) })
}
