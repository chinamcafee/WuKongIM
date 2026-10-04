package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// HistoryPolicy binds replica-lag handling and explicit recovery decisions.
// It never changes source leadership, combines histories, or weakens metadata checks.
type HistoryPolicy struct {
	LeaderQuorumPrefixes bool              `json:"leader_quorum_prefixes"`
	Recoveries           []HistoryRecovery `json:"recoveries,omitempty"`
}

type HistoryPrefixSelection struct {
	Policy        HistoryPolicy `json:"policy"`
	CaptureDigest string        `json:"capture_digest"`
	Channels      uint64        `json:"channels"`
	Accepted      uint64        `json:"accepted"`
	Unresolved    uint64        `json:"unresolved"`
	Recovered     uint64        `json:"recovered"`
	SHA256        string        `json:"sha256"`
}

type historyPrefixComparison struct {
	ctx       context.Context
	capture   SourceCapture
	w         Workspace
	decoder   HistoryPrefixDecoder
	policy    HistoryPolicy
	inspector *historyPrefixInspector
	// A fresh private namespace makes every invocation rebuild its own proofs.
	// Prior scratch verdicts and imported diagnostic reports are never trusted.
	proofs Workspace
}

func (c *historyPrefixComparison) accept(row sourceCandidate) error {
	if c.inspector == nil {
		var err error
		c.inspector, err = newHistoryPrefixInspector(c.ctx, c.capture, c.w, c.decoder)
		if err != nil {
			return err
		}
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return err
		}
		c.proofs = scopedWorkspace{Workspace: c.w, prefix: "selection-history-prefix/v1/" + hex.EncodeToString(nonce[:]) + "/"}
	}
	key := []byte(fmt.Sprintf("%020d", row.Identity.ChannelHash))
	data, found, err := c.proofs.Get(c.ctx, key)
	if err != nil {
		return err
	}
	var proof HistoryPrefixReport
	if found {
		if err = json.Unmarshal(data, &proof); err != nil {
			return err
		}
	} else {
		proof, err = c.inspector.channel(row.Identity.ChannelHash)
		if err != nil {
			return err
		}
		data, err = json.Marshal(proof)
		if err != nil {
			return err
		}
		if err = c.proofs.Put(c.ctx, []transfer.SpoolRow{{Key: key, Value: data}}); err != nil {
			return err
		}
	}
	recovered, err := c.recoveryNode(proof)
	if err != nil {
		return err
	}
	if recovered == 0 && (proof.Class != "leader_quorum_prefix" || proof.CandidateNode == 0) {
		return fmt.Errorf("source channel %s durable history remains unresolved: %v", proof.IdentitySHA256, proof.Reasons)
	}
	replicas := append([]uint64(nil), row.Group.Replicas...)
	slices.Sort(replicas)
	if row.Table != "Message" || proof.Leader != row.Group.Leader || !slices.Equal(proof.Replicas, replicas) || proof.IdentitySHA256 != diagnosticSHA([]byte(IdentityKey(row.Identity.Channel.ID, row.Identity.Channel.Type))) || !slices.Contains(row.Group.Replicas, row.NodeID) {
		return errors.New("source message candidate differs from its rebuilt history proof")
	}
	return nil
}

func (c *historyPrefixComparison) report() (*HistoryPrefixSelection, error) {
	if c.proofs == nil {
		if len(c.policy.Recoveries) > 0 {
			return nil, errors.New("history recovery decision was not applied")
		}
		return nil, nil
	}
	r := &HistoryPrefixSelection{Policy: c.policy, CaptureDigest: c.capture.Digest}
	h := sha256.New()
	enc := json.NewEncoder(h)
	err := c.proofs.Walk(c.ctx, nil, func(row transfer.SpoolRow) error {
		var p HistoryPrefixReport
		if err := json.Unmarshal(row.Value, &p); err != nil {
			return err
		}
		r.Channels++
		node, err := c.recoveryNode(p)
		if err != nil {
			return err
		}
		if node != 0 {
			r.Recovered++
		}
		if p.CandidateNode != 0 || node != 0 {
			r.Accepted++
		} else {
			r.Unresolved++
		}
		return enc.Encode(p)
	})
	if err != nil {
		return r, err
	}
	if r.Recovered != uint64(len(c.policy.Recoveries)) {
		return r, errors.New("history recovery decision was not applied")
	}
	r.SHA256 = hex.EncodeToString(h.Sum(nil))
	return r, nil
}

// source returns one complete original copy; it never merges replica rows.
func (c *historyPrefixComparison) source(row sourceCandidate) (uint64, error) {
	if err := c.accept(row); err != nil {
		return 0, err
	}
	for _, recovery := range c.policy.Recoveries {
		if recovery.Owner == row.Identity.ChannelHash {
			return recovery.SourceNode, nil
		}
	}
	return row.Group.Leader, nil
}
