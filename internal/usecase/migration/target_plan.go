package migration

import "time"

// TargetPlan describes a new cluster generation. Source Raft positions and
// replica membership must never be copied into this plan.
type TargetPlan struct {
	ClusterID       string       `json:"cluster_id"`
	CreatedAt       time.Time    `json:"created_at"`
	SlotCount       uint32       `json:"slot_count"`
	HashSlotCount   uint16       `json:"hash_slot_count"`
	Replicas        uint16       `json:"replicas"`
	ChannelReplicas uint16       `json:"channel_replicas"`
	Nodes           []TargetNode `json:"nodes"`
}

// TargetNode binds a new native data directory to its deployment node identity
// and RPC endpoint. DataDir is an offline output path, never an existing server.
type TargetNode struct {
	NodeID  uint64 `json:"node_id"`
	Addr    string `json:"addr"`
	DataDir string `json:"data_dir"`
}
