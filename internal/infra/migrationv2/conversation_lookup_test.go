package migrationv2_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// addConversationCopy preserves the exact original unique index and creates a
// later physical row, as original repeated writes could do. Only a Leader's
// list chooses the later version; followers must still agree on exact state.
func addConversationCopy(t *testing.T, source string, node uint64, mode string) (migration.Row, bool, uint64) {
	t.Helper()
	var original migration.Row
	snap, err := migrationv2.ReadStoppedNode(context.Background(), migration.NodeOptions{NodeID: node, Options: migration.Options{DataDir: source, ShardCount: 2}}, func(r migration.Row) error {
		if r.Table == "Conversation" && r.Kind == migration.Primary && string(r.Fields["ChannelId"]) == "migrationgroup" && original.ID == 0 {
			original = r
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.NotZero(t, original.ID)
	slot := crc32.ChecksumIEEE(original.Fields["Uid"]) % snap.Config.SlotCount
	leader := false
	for _, s := range snap.Config.Slots {
		if s.ID == slot {
			leader = s.Leader == node
		}
	}
	later := original.ID + 100000 + node
	stamp := binary.BigEndian.Uint64(original.Fields["UpdatedAt"]) + 100 + node
	changed := rewriteOriginalIndexFixture(t, source, func(k, v []byte, b *pebble.Batch) bool {
		if len(k) != 22 || !bytes.Equal(k[:20], original.Key) {
			return false
		}
		col := binary.BigEndian.Uint16(k[20:])
		if (mode == "leader_tie" && leader || mode == "follower_tie" && !leader) && col == 0x0908 {
			stampBytes := make([]byte, 8)
			binary.BigEndian.PutUint64(stampBytes, stamp)
			require.NoError(t, b.Set(k, stampBytes, nil))
		}
		if mode == "replica_state_conflict" && !leader && col == 0x0906 {
			v = make([]byte, 8)
			binary.BigEndian.PutUint64(v, 999)
			require.NoError(t, b.Set(k, v, nil))
		}
		binary.BigEndian.PutUint64(k[12:], later)
		if col == 0x0908 {
			v = make([]byte, 8)
			binary.BigEndian.PutUint64(v, stamp)
		}
		if col == 0x0907 {
			v = make([]byte, 8)
			binary.BigEndian.PutUint64(v, binary.BigEndian.Uint64(original.Fields["CreatedAt"])+node)
		}
		if ((mode == "list_state_conflict" || mode == "leader_tie") && leader || mode == "follower_tie" && !leader) && col == 0x0906 {
			v = make([]byte, 8)
			binary.BigEndian.PutUint64(v, 999)
		}
		require.NoError(t, b.Set(k, v, nil))
		return true
	})
	require.Greater(t, changed, 1)
	return original, leader, stamp
}

func conversationPolicy() *migration.MetadataPolicy {
	return &migration.MetadataPolicy{DeviceLookup: "v2_cold_start", ConversationLookup: "v2_active_slot", ConversationListLimit: 1000}
}

func TestConversationLookupArchivePreservesLeaderVersionAndExactState(t *testing.T) {
	for _, tc := range []struct{ sources, targets int }{{1, 1}, {3, 1}, {3, 3}} {
		t.Run(fmt.Sprintf("%d_to_%d_node_cluster", tc.sources, tc.targets), func(t *testing.T) {
			ctx := context.Background()
			r := migrationv2.Reader{}
			var sources []migration.NodeOptions
			var expected migration.Row
			var expectedVersion uint64
			for n := 1; n <= tc.sources; n++ {
				name := "original-v2-server.tar.gz"
				if tc.sources > 1 {
					name = fmt.Sprintf("original-v2-three-%d.tar.gz", n)
				}
				dir := unpackNamedFixture(t, name)
				clearFixtureMessageExtensions(t, dir)
				row, leader, version := addConversationCopy(t, dir, uint64(n), "")
				if leader {
					expected, expectedVersion = row, version
				}
				sources = append(sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
			}
			require.NotZero(t, expected.ID)
			plan := diagnosticPlan(t, sources[0].DataDir)
			plan.Sources = sources
			plan.Target.Replicas, plan.Target.ChannelReplicas = uint16(tc.targets), uint16(tc.targets)
			for n := 2; n <= tc.targets; n++ {
				plan.Target.Nodes = append(plan.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + n), Addr: fmt.Sprintf("127.0.0.1:%d", 57930+n), DataDir: filepath.Join(t.TempDir(), "target")})
			}
			open := func() *transfer.Spool {
				w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, w.Close()) })
				return w
			}
			_, err := migration.Prepare(ctx, plan, open(), r, r, nil)
			require.Error(t, err)
			plan.Metadata = conversationPolicy()
			w := open()
			prepared, err := migration.Prepare(ctx, plan, w, r, r, nil)
			require.NoError(t, err)
			require.EqualValues(t, tc.sources, prepared.Selection.Metadata.Conversations.DuplicateGroups)
			require.EqualValues(t, tc.sources, prepared.Selection.Metadata.Conversations.ShadowedRows)
			found := 0
			require.NoError(t, migration.WalkSelectedSources(ctx, w, func(rec migration.SelectedRecord) error {
				if rec.Row.Table == "Conversation" && bytes.Equal(rec.Row.Fields["Uid"], expected.Fields["Uid"]) && rec.Identity.Channel.ID == "migrationgroup" {
					found++
					require.Equal(t, expected.Fields["ReadedToMsgSeq"], rec.Row.Fields["ReadedToMsgSeq"])
					require.Equal(t, expected.Fields["DeletedAtMsgSeq"], rec.Row.Fields["DeletedAtMsgSeq"])
					require.Equal(t, expectedVersion, binary.BigEndian.Uint64(rec.Row.Fields["UpdatedAt"]))
				}
				return nil
			}))
			require.Equal(t, 1, found)
			archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
			require.NoError(t, err)
			_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
			require.NoError(t, err)
			for _, source := range sources {
				require.NoError(t, os.Rename(source.DataDir, source.DataDir+"-unmounted"))
			}
			fresh := open()
			rebuilt, err := migration.PrepareArchive(ctx, plan, fresh, r, archive)
			require.NoError(t, err)
			require.Equal(t, prepared.Selection, rebuilt.Selection)
			require.NoError(t, migrationv3.Install(ctx, plan.Target, rebuilt.Conversion, fresh))
			verified, err := migration.VerifyTargets(ctx, plan.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
			require.NoError(t, err)
			require.Equal(t, "offline_verified", verified.Status)
		})
	}
}

func TestConversationLookupRejectsUnsafeReduction(t *testing.T) {
	for _, mode := range []string{"missing_index", "wrong_primary", "list_state_conflict", "leader_tie", "list_limit", "pending_limit"} {
		t.Run(mode, func(t *testing.T) {
			dir := compatibleMessageFixture(t)
			original, _, _ := addConversationCopy(t, dir, 1, mode)
			if mode == "missing_index" || mode == "wrong_primary" {
				changed := rewriteOriginalIndexFixture(t, dir, func(k, v []byte, b *pebble.Batch) bool {
					if len(k) != 22 || !bytes.Equal(k[:6], []byte{9, 1, byte(migration.Index), 0, 9, 1}) || binary.BigEndian.Uint64(v) != original.ID {
						return false
					}
					if mode == "missing_index" {
						require.NoError(t, b.Delete(k, nil))
					} else {
						binary.BigEndian.PutUint64(v, original.ID-1)
						require.NoError(t, b.Set(k, v, nil))
					}
					return true
				})
				require.Equal(t, 1, changed)
			}
			plan := diagnosticPlan(t, dir)
			plan.Metadata = conversationPolicy()
			if mode == "list_limit" {
				plan.Metadata.ConversationListLimit = 1
			}
			if mode == "pending_limit" {
				plan.Metadata.ConversationListLimit = 2
				raw := fmt.Sprintf(`[{"channel_id":"synthetic-absent","channel_type":2,"user_read_seqs":{%q:0},"tag_key":"synthetic","last_msg_seq":1}]`, string(original.Fields["Uid"]))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "conversationv2", "conversations.json"), []byte(raw), 0600))
			}
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			prepared, err := migration.Prepare(context.Background(), plan, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
			require.Error(t, err)
			require.Empty(t, prepared.Selection.Digest)
			if mode == "list_state_conflict" {
				require.ErrorContains(t, err, "list and exact lookup disagree")
			}
			if mode == "list_limit" || mode == "pending_limit" {
				require.ErrorContains(t, err, "pre-deduplication limit")
			}
		})
	}
}

func TestConversationLookupChecksExactReplicaStateInsteadOfFollowerList(t *testing.T) {
	for _, mode := range []string{"follower_tie", "replica_state_conflict"} {
		t.Run(mode, func(t *testing.T) {
			var sources []migration.NodeOptions
			for n := 1; n <= 3; n++ {
				dir := unpackNamedFixture(t, fmt.Sprintf("original-v2-three-%d.tar.gz", n))
				clearFixtureMessageExtensions(t, dir)
				addConversationCopy(t, dir, uint64(n), mode)
				sources = append(sources, migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}})
			}
			plan := diagnosticPlan(t, sources[0].DataDir)
			plan.Sources = sources
			plan.Metadata = conversationPolicy()
			w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), plan.Digest(), 128<<20)
			require.NoError(t, err)
			defer w.Close()
			_, err = migration.Prepare(context.Background(), plan, w, migrationv2.Reader{}, migrationv2.Reader{}, nil)
			if mode == "follower_tie" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "Conversation record conflicts between replica nodes")
			}
		})
	}
}
