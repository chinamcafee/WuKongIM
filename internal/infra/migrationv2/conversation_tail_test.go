package migrationv2_test

import (
	"bytes"
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

func TestOriginalEmptyUIDRecoveryIsArchivedWithoutCreatingConversation(t *testing.T) {
	// SourceCommit manager_conversation.go storeConversations explicitly skips
	// empty UID entries, including after loadFromFile; replay that exact shape.
	source := compatibleMessageFixture(t)
	path := filepath.Join(source, "conversationv2", "conversations.json")
	raw := []byte(`{"channel_id":"migrationgroup","channel_type":2,"user_read_seqs":{"":99},"tag_key":"synthetic-cache","last_msg_seq":3}`)
	require.NoError(t, os.WriteFile(path, append(append([]byte{'['}, raw...), ']'), 0600))
	before := fileDigests(t, source)
	var archived migrationv2.Row
	_, err := (migrationv2.Reader{}).ReadStoppedNode(context.Background(), migration.NodeOptions{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}, func(row migrationv2.Row) error {
		if row.Table == "IgnoredConversation" {
			archived = row
		}
		require.NotEqual(t, "PendingConversation", row.Table)
		return nil
	}, func(migration.SourceFile) error { return nil })
	require.NoError(t, err)
	require.Equal(t, raw, archived.Value)
	id, err := migrationv2.Identify(archived)
	require.NoError(t, err)
	require.Empty(t, id.UID)
	require.Empty(t, id.Channel.ID)
	for _, change := range []func(*migrationv2.Row){
		func(r *migrationv2.Row) { r.Key[0] ^= 1 },
		func(r *migrationv2.Row) {
			r.Value = bytes.ReplaceAll(r.Value, []byte(`"":99`), []byte(`"real-user":99`))
		},
	} {
		r := archived
		r.Key = bytes.Clone(r.Key)
		r.Value = bytes.Clone(r.Value)
		change(&r)
		_, err := migrationv2.Identify(r)
		require.Error(t, err)
	}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "ignored-empty-uid", 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	plan := migration.Plan{Version: 1, SourceCommit: migrationv2.SourceCommit, Sources: []migration.NodeOptions{{NodeID: 1, Options: migration.Options{DataDir: source, ShardCount: 2}}}, Target: migration.TargetPlan{ClusterID: "ignored-empty-uid", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: 1, ChannelReplicas: 1, Nodes: []migration.TargetNode{{NodeID: 1, Addr: "127.0.0.1:57881", DataDir: filepath.Join(t.TempDir(), "target")}}}}
	result, err := migration.Prepare(context.Background(), plan, w, r, r, nil)
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.Selection.Preserved["originally_ignored_empty_uid_conversation"])
	require.Equal(t, uint64(4), result.Conversion.Messages)
	require.NoError(t, migration.WalkTargetMetadata(context.Background(), w, func(row migration.TargetRecord) error {
		if row.Table == "membership" || row.Table == "cmd_membership" {
			var m struct {
				UID string `json:"uid"`
			}
			require.NoError(t, migration.UnmarshalState(row.Value, &m))
			require.NotEmpty(t, m.UID)
		}
		return nil
	}))
	require.Equal(t, before, fileDigests(t, source))
}
