package migrationv2_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

type interruptedImport struct {
	migration.Workspace
	remaining    int
	phase        string
	messageScans int
}

func (w *interruptedImport) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	if w.phase == "messages" && strings.HasPrefix(string(prefix), "target/messages/") {
		w.messageScans++
		if w.messageScans == 2 {
			return context.Canceled
		}
	}
	return w.Workspace.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		if w.phase == "metadata" && string(prefix) == "target/meta/" {
			w.remaining--
			if w.remaining == 0 {
				return context.Canceled
			}
		}
		return visit(row)
	})
}

func TestNativeImportResumesSamePlanAndProtectsModifiedCompletedData(t *testing.T) {
	for _, phase := range []string{"metadata", "messages"} {
		t.Run(phase, func(t *testing.T) { testNativeImportResume(t, phase) })
	}
}

func testNativeImportResume(t *testing.T, phase string) {
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "resume-fixture", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: compatibleMessageFixture(t), ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "resume-fixture", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57884", DataDir: filepath.Join(t.TempDir(), "node")}}}}
	p, err := migration.Prepare(ctx, plan, w, r, r, nil)
	require.NoError(t, err)
	err = migrationv3.Install(ctx, plan.Target, p.Conversion, &interruptedImport{Workspace: w, remaining: 4, phase: phase})
	require.True(t, errors.Is(err, context.Canceled), "%v", err)
	changed := plan.Target
	changed.ClusterID = "different-generation"
	require.Error(t, migrationv3.Install(ctx, changed, p.Conversion, w))
	require.NoError(t, migrationv3.Install(ctx, plan.Target, p.Conversion, w))
	require.NoError(t, migrationv3.Install(ctx, plan.Target, p.Conversion, w))
	_, err = migration.VerifyTargets(ctx, plan.Target, p.Selection, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	db, err := meta.Open(filepath.Join(plan.Target.Nodes[0].DataDir, "slotmeta"))
	require.NoError(t, err)
	require.NoError(t, db.MetaDB().HashSlot(0).UpsertUser(ctx, meta.User{UID: "new-production-user"}))
	require.NoError(t, db.Close())
	require.ErrorContains(t, migrationv3.Install(ctx, plan.Target, p.Conversion, w), "changed")
	_, err = migration.VerifyTargets(ctx, plan.Target, p.Selection, w, r, migrationv3.Inspector{})
	require.ErrorContains(t, err, "user target row count")
}
