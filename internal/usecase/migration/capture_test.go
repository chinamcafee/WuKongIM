package migration

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCapturedAuthoritySeparatesOriginalOfflineStatisticsFromConsensus(t *testing.T) {
	makeNodes := func() []NodeSnapshot {
		var nodes []NodeSnapshot
		for _, id := range []uint64{1, 2} {
			nodes = append(nodes, NodeSnapshot{NodeID: id,
				Config:         SourceConfig{Version: 1, SlotCount: 1, Nodes: []SourceNode{{ID: 1, Online: true}, {ID: 2, Online: true}}, Slots: []SourceSlot{{ID: 0, Leader: 1, Replicas: []uint64{1, 2}, Term: 1}}},
				ConfigProgress: LogProgress{LastIndex: 1, AppliedIndex: 1},
				SlotProgress:   []LogProgress{{Group: "0", FirstIndex: 1, LastIndex: 1, AppliedIndex: 1, LastTerm: 1, LastDigest: "same", LogDigest: "same"}},
			})
		}
		return nodes
	}
	nodes := makeNodes()
	nodes[0].Config.Nodes[0].OfflineCount = 3
	nodes[1].Config.Nodes[0].OfflineCount = 10
	nodes[0].Config.Nodes[0].LastOffline = 123
	nodes[1].Config.Nodes[0].LastOffline = 456
	before, err := json.Marshal(nodes)
	require.NoError(t, err)
	require.NoError(t, validateCapturedAuthority(nodes))
	after, err := json.Marshal(nodes)
	require.NoError(t, err)
	require.Equal(t, before, after, "original statistics must remain in captured provenance")
	for name, change := range map[string]func([]NodeSnapshot){
		"online-state":   func(n []NodeSnapshot) { n[1].Config.Nodes[0].Online = false },
		"leader":         func(n []NodeSnapshot) { n[1].Config.Slots[0].Leader = 2 },
		"replica-set":    func(n []NodeSnapshot) { n[1].Config.Slots[0].Replicas = []uint64{2} },
		"slot-term":      func(n []NodeSnapshot) { n[1].Config.Slots[0].Term++ },
		"config-version": func(n []NodeSnapshot) { n[1].Config.Version++ },
		"unapplied-log":  func(n []NodeSnapshot) { n[0].SlotProgress[0].LastIndex++ },
		"log-digest":     func(n []NodeSnapshot) { n[1].SlotProgress[0].LogDigest = "different" },
	} {
		t.Run(name, func(t *testing.T) {
			n := makeNodes()
			change(n)
			require.Error(t, validateCapturedAuthority(n))
		})
	}
}
