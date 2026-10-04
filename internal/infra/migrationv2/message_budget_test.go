package migrationv2_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

// The source-decoding port supplies an otherwise valid message whose body alone
// fills a native recovery page. Header and identity bytes make it indivisible.
type oversizedSourceMessage struct{ migrationv2.Reader }

func (r oversizedSourceMessage) DecodeBusiness(row migration.Row, id migration.RecordIdentity) (migration.BusinessFacts, error) {
	facts, err := r.Reader.DecodeBusiness(row, id)
	if facts.Message != nil {
		facts.Message.Payload = make([]byte, 1<<20)
	}
	return facts, err
}

func TestMigrationPrepareRejectsUnrecoverableSourceMessage(t *testing.T) {
	ctx := context.Background()
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "oversized-message", 128<<20)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Close()) })
	targetDir := filepath.Join(t.TempDir(), "unpublished-target")
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit,
		Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: unpackNamedFixture(t, "original-v2-server.tar.gz"), ShardCount: 2}}},
		Target:  migration.TargetPlan{ClusterID: "oversized-message", CreatedAt: time.Unix(1700000000, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 1, Addr: "127.0.0.1:17001", DataDir: targetDir}}}}
	r := oversizedSourceMessage{}
	_, err = migration.Prepare(ctx, plan, w, r, r, nil)
	require.ErrorContains(t, err, "native recovery page budget")
	for _, key := range []string{"conversion/COMPLETE", "workflow/PREPARED"} {
		_, found, err := w.Get(ctx, []byte(key))
		require.NoError(t, err)
		require.False(t, found, key)
	}
	_, err = os.Stat(targetDir)
	require.ErrorIs(t, err, os.ErrNotExist)
}
