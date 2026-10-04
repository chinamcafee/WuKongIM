package migrationv2_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

// This synthetic decoding-port fixture isolates a supported native field shape.
// It is not a replacement for the immutable original-v2 acceptance fixtures.
type emptySenderSource struct{ migrationv2.Reader }

func (r emptySenderSource) DecodeBusiness(row migration.Row, id migration.RecordIdentity) (migration.BusinessFacts, error) {
	facts, err := r.Reader.DecodeBusiness(row, id)
	if facts.Message != nil {
		facts.Message.FromUID = ""
	}
	return facts, err
}

func TestNativeImportAndIndependentVerificationPreserveEmptySenders(t *testing.T) {
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "empty-sender", 128<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Close()) })
	r := emptySenderSource{}
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: compatibleMessageFixture(t), ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "empty-sender", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 101, Addr: "127.0.0.1:57884", DataDir: filepath.Join(t.TempDir(), "node")}}}}
	prepared, err := migration.Prepare(ctx, plan, w, r, r, nil)
	require.NoError(t, err)
	require.NoError(t, migrationv3.Install(ctx, plan.Target, prepared.Conversion, w))
	_, err = migration.VerifyTargets(ctx, plan.Target, prepared.Selection, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
}
