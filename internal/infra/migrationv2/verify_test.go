package migrationv2_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func TestIndependentVerificationDetectsAnAlteredCredential(t *testing.T) {
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "verify-fixture", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: compatibleMessageFixture(t), ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "verify-fixture", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57882", DataDir: filepath.Join(t.TempDir(), "node")}}}}
	p, err := migration.Prepare(ctx, plan, w, r, r, nil)
	require.NoError(t, err)
	require.NoError(t, migrationv3.Install(ctx, plan.Target, p.Conversion, w))
	report, err := migration.VerifyTargets(ctx, plan.Target, p.Selection, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, "offline_verified", report.Status)
	require.False(t, report.CutoverReady)
	require.Equal(t, uint64(4), report.Messages)
	db, err := meta.Open(filepath.Join(plan.Target.Nodes[0].DataDir, "slotmeta"))
	require.NoError(t, err)
	// A wrong token leaves row counts, message checksums and target topology intact.
	for slot := uint16(0); slot < 256; slot++ {
		s := db.MetaDB().HashSlot(slot)
		device, found, err := s.GetDevice(ctx, "migrationbob", 1)
		require.NoError(t, err)
		if found {
			device.Token = "altered-secret"
			require.NoError(t, s.UpsertDevice(ctx, device))
		}
	}
	require.NoError(t, db.Close())
	_, err = migration.VerifyTargets(ctx, plan.Target, p.Selection, w, r, migrationv3.Inspector{})
	require.ErrorContains(t, err, "device")
	require.NotContains(t, err.Error(), "altered-secret")
	require.NotContains(t, err.Error(), "synthetic-migrationbob")
}

func TestIndependentVerificationRejectsCorruptBootstrapSnapshot(t *testing.T) {
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "verify-fixture", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: compatibleMessageFixture(t), ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "verify-fixture", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57882", DataDir: filepath.Join(t.TempDir(), "node")}}}}
	p, err := migration.Prepare(ctx, plan, w, r, r, nil)
	require.NoError(t, err)
	require.NoError(t, migrationv3.Install(ctx, plan.Target, p.Conversion, w))
	report, err := migration.VerifyTargets(ctx, plan.Target, p.Selection, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, "offline_verified", report.Status)
	require.False(t, report.CutoverReady)
	require.Equal(t, uint64(4), report.Messages)

	files, err := filepath.Glob(filepath.Join(plan.Target.Nodes[0].DataDir, "controller", "raft", "snap", "*.snap"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.NoError(t, os.WriteFile(files[0], []byte("corrupt-bootstrap"), 0600))
	_, err = migration.VerifyTargets(ctx, plan.Target, p.Selection, w, r, migrationv3.Inspector{})
	require.Error(t, err, "typed business rows alone must not certify an unreadable native bootstrap")
}
