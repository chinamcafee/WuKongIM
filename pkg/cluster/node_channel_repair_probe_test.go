package cluster

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// coldRepairRuntime models a durable replica whose reactor is not loaded yet.
type coldRepairRuntime struct {
	migrationRuntimeChannelService
	calls int
}

func (r *coldRepairRuntime) RuntimeProbe(context.Context, ch.RuntimeSelector) (ch.RuntimeProbeResult, error) {
	r.calls++
	if len(r.applied) == 0 {
		return ch.RuntimeProbeResult{}, nil
	}
	m := r.applied[len(r.applied)-1]
	return ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{{ChannelID: m.ID, ChannelEpoch: m.Epoch, LeaderEpoch: m.LeaderEpoch, Role: ch.RoleFollower, Status: m.Status, LEO: 17, HW: 12, CheckpointHW: 12}}}, nil
}

func TestChannelRepairProbeActivatesColdAuthoritativeReplica(t *testing.T) {
	n, db := newLocalMetadataScanNode(t)
	n.defaultSlotProposer = fixedUnitChannelMigrationSlotRuntime{localLeader: true}
	id := ch.ChannelID{ID: keyForNodeHashSlot(t, 4, 0), Type: 2}
	meta := metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 4, LeaderEpoch: 5, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2, Status: uint8(ch.StatusActive)})
	if err := db.ForHashSlot(0).UpsertChannelRuntimeMeta(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	runtime := &coldRepairRuntime{}
	n.channels = runtime
	got, err := n.ProbeChannel(context.Background(), 1, id.ID, id.Type)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChannelID != id || got.HW != 12 || got.LeaderEpoch != 5 || len(runtime.applied) != 1 || runtime.calls != 2 {
		t.Fatalf("probe=%+v applied=%+v calls=%d", got, runtime.applied, runtime.calls)
	}
}

func TestChannelRepairProbeDoesNotActivateNonReplica(t *testing.T) {
	n, db := newLocalMetadataScanNode(t)
	n.defaultSlotProposer = fixedUnitChannelMigrationSlotRuntime{localLeader: true}
	id := ch.ChannelID{ID: keyForNodeHashSlot(t, 4, 0), Type: 2}
	meta := metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 4, LeaderEpoch: 5, Leader: 2, Replicas: []uint64{2, 3}, ISR: []uint64{2, 3}, MinISR: 2, Status: uint8(ch.StatusActive)})
	if err := db.ForHashSlot(0).UpsertChannelRuntimeMeta(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	runtime := &coldRepairRuntime{}
	n.channels = runtime
	_, err := n.ProbeChannel(context.Background(), 1, id.ID, id.Type)
	if !errors.Is(err, ch.ErrChannelNotFound) || len(runtime.applied) != 0 {
		t.Fatalf("err=%v applied=%+v", err, runtime.applied)
	}
}
