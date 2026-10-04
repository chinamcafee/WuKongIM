package cluster

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// OfflineImportSeal binds an installed native generation to its deployment
// configuration. It is not a v2 consensus or external-delivery certificate.
type OfflineImportSeal struct {
	Version         int                 `json:"version"`
	ClusterID       string              `json:"cluster_id"`
	NodeID          uint64              `json:"node_id"`
	SlotCount       uint32              `json:"slot_count"`
	HashSlotCount   uint16              `json:"hash_slot_count"`
	Replicas        uint16              `json:"replicas"`
	ChannelReplicas uint16              `json:"channel_replicas"`
	PlanSHA256      string              `json:"plan_sha256"`
	SourceSHA256    string              `json:"source_sha256"`
	MaxMessageID    uint64              `json:"max_message_id"`
	Nodes           []OfflineImportNode `json:"nodes"`
}

type OfflineImportNode struct {
	NodeID uint64 `json:"node_id"`
	Addr   string `json:"addr"`
}

// ReadOfflineImportSeal accepts only a completely published offline import.
// Ordinary clusters without either marker retain their usual startup behavior.
func ReadOfflineImportSeal(dataDir string) (seal OfflineImportSeal, found bool, err error) {
	read := func(name string) ([]byte, error) {
		path := filepath.Join(dataDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return nil, errors.New("invalid offline import marker")
		}
		return os.ReadFile(path)
	}
	started, startErr := read("MIGRATION-IMPORTING")
	complete, completeErr := read("MIGRATION-COMPLETE")
	if errors.Is(startErr, os.ErrNotExist) && errors.Is(completeErr, os.ErrNotExist) {
		return seal, false, nil
	}
	if startErr != nil || completeErr != nil {
		return seal, true, fmt.Errorf("offline import is incomplete or its markers conflict: %w", errors.Join(startErr, completeErr))
	}
	if !bytes.Equal(started, complete) {
		return seal, true, errors.New("offline import markers conflict")
	}
	d := json.NewDecoder(bytes.NewReader(complete))
	d.DisallowUnknownFields()
	if err := d.Decode(&seal); err != nil {
		return seal, true, fmt.Errorf("offline import seal: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return seal, true, errors.New("trailing offline import seal data")
	}
	validSHA := func(v string) bool { b, err := hex.DecodeString(v); return err == nil && len(b) == 32 }
	if seal.Version != 1 || seal.NodeID == 0 || seal.ClusterID == "" || seal.HashSlotCount != 256 || seal.SlotCount == 0 || seal.SlotCount > 256 || seal.Replicas == 0 || seal.ChannelReplicas == 0 || !validSHA(seal.PlanSHA256) || !validSHA(seal.SourceSHA256) || len(seal.Nodes) == 0 || len(seal.Nodes) > 1024 {
		return seal, true, errors.New("invalid offline import seal")
	}
	return seal, true, nil
}

func validateOfflineImportConfig(cfg Config) error {
	seal, found, err := ReadOfflineImportSeal(cfg.DataDir)
	if err != nil || !found {
		return err
	}
	if seal.NodeID != cfg.NodeID || seal.ClusterID != cfg.Control.ClusterID || seal.SlotCount != cfg.Slots.InitialSlotCount || seal.HashSlotCount != cfg.Slots.HashSlotCount || seal.Replicas != cfg.Slots.ReplicaCount || seal.ChannelReplicas != cfg.Channel.ReplicaCount || len(seal.Nodes) != len(cfg.Control.Voters) {
		return errors.New("offline import configuration differs from installed generation")
	}
	seen := map[uint64]bool{}
	for _, node := range seal.Nodes {
		if node.NodeID == 0 || node.Addr == "" || seen[node.NodeID] {
			return errors.New("invalid offline import node set")
		}
		seen[node.NodeID] = true
		matched := false
		for _, voter := range cfg.Control.Voters {
			if voter.NodeID == node.NodeID && voter.Addr == node.Addr {
				matched = true
			}
		}
		if !matched {
			return errors.New("offline import voter endpoints differ from installed generation")
		}
	}
	if !seen[cfg.NodeID] {
		return errors.New("offline import node is absent from generation")
	}
	return nil
}
