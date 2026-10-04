package migration

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
)

// HistoryRecovery is an operator decision for one inspected empty-leader
// channel. All digests bind original evidence before exclusions or renumbering.
// SourceNode explicitly names one complete formal replica; no election occurs.
type HistoryRecovery struct {
	Owner          uint64 `json:"owner_hash,string"`
	IdentitySHA256 string `json:"identity_sha256"`
	CaptureDigest  string `json:"capture_digest"`
	ProofDigest    string `json:"proof_digest"`
	SourceNode     uint64 `json:"source_node,string"`
	Messages       uint64 `json:"messages"`
	HistorySHA256  string `json:"history_sha256"`
}

func validateHistoryPolicy(p *HistoryPolicy) error {
	if p == nil {
		return nil
	}
	if len(p.Recoveries) > 1024 || (len(p.Recoveries) > 0 && !p.LeaderQuorumPrefixes) {
		return errors.New("history recovery requires bounded explicit decisions and history comparison")
	}
	seen := make(map[uint64]bool)
	for _, r := range p.Recoveries {
		if r.Owner == 0 || r.SourceNode == 0 || r.Messages == 0 || seen[r.Owner] {
			return errors.New("invalid or duplicate history recovery decision")
		}
		seen[r.Owner] = true
		for _, digest := range []string{r.IdentitySHA256, r.CaptureDigest, r.ProofDigest, r.HistorySHA256} {
			b, err := hex.DecodeString(digest)
			if err != nil || len(b) != 32 || hex.EncodeToString(b) != digest {
				return errors.New("history recovery requires exact lowercase SHA256 digests")
			}
		}
	}
	return nil
}

// recoveryNode checks a freshly rebuilt diagnostic without changing its verdict
// or claiming historical client acknowledgement. Unknown failures stay blocked.
func (c *historyPrefixComparison) recoveryNode(p HistoryPrefixReport) (uint64, error) {
	for _, r := range c.policy.Recoveries {
		if r.Owner != p.Owner {
			continue
		}
		invalid := func() (uint64, error) {
			return 0, fmt.Errorf("history recovery evidence mismatch for channel %s", r.IdentitySHA256)
		}
		if p.Version != 1 || p.Class != "unresolved" || p.CandidateNode != 0 || p.HistoricalACKProven ||
			p.CaptureDigest != r.CaptureDigest || p.Digest != r.ProofDigest || p.IdentitySHA256 != r.IdentitySHA256 ||
			p.Leader == r.SourceNode || !slices.Contains(p.Replicas, r.SourceNode) ||
			!slices.Contains(p.CompleteNodes, r.SourceNode) || len(p.CompleteNodes) <= len(p.Replicas)/2 ||
			!slices.Contains(p.Reasons, "configured_leader_is_not_complete") {
			return invalid()
		}
		for _, reason := range p.Reasons {
			switch reason {
			case "configured_leader_is_not_complete", "leader_changed_without_new_term", "retained_membership_change", "unsupported_retained_configuration":
			default:
				return invalid()
			}
		}
		leaderEmpty, sourceComplete := false, false
		for _, h := range p.Histories {
			if h.NodeID == p.Leader {
				leaderEmpty = h.Messages == 0 && h.Last == 0 && !h.TailPresent
			}
			if h.NodeID == r.SourceNode {
				sourceComplete = h.Messages == r.Messages && h.Last == r.Messages && h.TailPresent && h.Tail == h.Last &&
					h.MissingSuffix == 0 && h.PrefixConflicts == 0 && h.SHA256 == r.HistorySHA256
			}
		}
		if !leaderEmpty || !sourceComplete {
			return invalid()
		}
		return r.SourceNode, nil
	}
	return 0, nil
}
