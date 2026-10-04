package migrationv2_test

import (
	"context"
	"encoding/binary"
	"fmt"
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

// lagPrefixFixture removes only a suffix and its exact original indexes from a
// private unpacked follower fixture. The complete leader/peer copies stay intact.
func lagPrefixFixture(t *testing.T, dir string, owner uint64, lengths ...uint64) {
	last := uint64(1)
	if len(lengths) > 0 {
		last = lengths[0]
	}
	t.Helper()
	changed := rewriteOriginalIndexFixture(t, dir, func(k, v []byte, b *pebble.Batch) bool {
		if len(k) < 4 || binary.BigEndian.Uint16(k) != 0x0101 {
			return false
		}
		if len(k) == 12 && k[2] == 4 && binary.BigEndian.Uint64(k[4:]) == owner {
			if last == 0 {
				require.NoError(t, b.Delete(k, nil))
				return true
			}
			binary.BigEndian.PutUint64(v, last)
			require.NoError(t, b.Set(k, v, nil))
			return true
		}
		drop := len(k) == 22 && k[2] == 1 && binary.BigEndian.Uint64(k[4:12]) == owner && binary.BigEndian.Uint64(k[12:20]) > last
		drop = drop || (len(k) == 14 && k[2] == 2 && len(v) == 16 && binary.BigEndian.Uint64(v[:8]) == owner && binary.BigEndian.Uint64(v[8:]) > last)
		drop = drop || (len(k) == 30 && (k[2] == 2 || k[2] == 3) && binary.BigEndian.Uint64(k[14:22]) == owner && binary.BigEndian.Uint64(k[22:]) > last)
		if drop {
			require.NoError(t, b.Delete(k, nil))
		}
		return drop
	})
	require.Greater(t, changed, 1)
}

func TestHistoryPrefixPrepareArchiveRebuildAndNativeImport(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, targets := range []int{1, 3} {
			t.Run(fmt.Sprintf("three_source_to_%d_node_cluster_recovery_%t", targets, recovery), func(t *testing.T) {
				ctx, r := context.Background(), migrationv2.Reader{}
				p := diagnosticPlan(t, "")
				p.Sources = nil
				p.Target.Nodes = nil
				p.Target.Replicas = uint16(targets)
				p.Target.ChannelReplicas = uint16(targets)
				var owner, leader uint64
				for n := 1; n <= 3; n++ {
					dir := unpackNamedFixture(t, fmt.Sprintf("original-v2-three-%d.tar.gz", n))
					clearFixtureMessageExtensions(t, dir)
					opt := migration.NodeOptions{NodeID: uint64(n), Options: migration.Options{DataDir: dir, ShardCount: 2}}
					_, err := r.ReadStoppedNode(ctx, opt, func(row migration.Row) error {
						if row.Table == "ChannelClusterConfig" && row.Kind == migration.Primary && string(row.Fields["ChannelId"]) == "migrationgroup" {
							c, e := r.InspectChannelConfig(row)
							if e != nil {
								return e
							}
							owner, leader = c.Owner, c.Leader
						}
						return nil
					}, nil)
					require.NoError(t, err)
					p.Sources = append(p.Sources, opt)
				}
				require.NotZero(t, owner)
				require.NotZero(t, leader)
				for _, n := range p.Sources {
					if recovery && n.NodeID == leader {
						lagPrefixFixture(t, n.DataDir, owner, 0)
						break
					}
					if !recovery && n.NodeID != leader {
						lagPrefixFixture(t, n.DataDir, owner)
						break
					}
				}
				before := map[uint64]map[string][32]byte{}
				for _, n := range p.Sources {
					before[n.NodeID] = fileDigests(t, n.DataDir)
				}
				for n := 1; n <= targets; n++ {
					p.Target.Nodes = append(p.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + n), Addr: fmt.Sprintf("127.0.0.1:%d", 58400+n), DataDir: filepath.Join(t.TempDir(), "target")})
				}
				strict, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "strict"), p.Digest(), 128<<20)
				require.NoError(t, err)
				blocked, err := migration.Prepare(ctx, p, strict, r, r, nil)
				require.Error(t, err)
				require.Empty(t, blocked.Selection.Digest)
				require.Nil(t, blocked.Selection.HistoryPrefixes)
				p.History = &migration.HistoryPolicy{LeaderQuorumPrefixes: true}
				if recovery {
					require.NoError(t, migration.InspectCapturedHistoryPrefixes(ctx, blocked.Capture, strict, r, []uint64{owner}, func(proof migration.HistoryPrefixReport) error {
						require.Equal(t, "unresolved", proof.Class)
						require.NotEmpty(t, proof.CompleteNodes)
						node := proof.CompleteNodes[0]
						for _, h := range proof.Histories {
							if h.NodeID == node {
								p.History.Recoveries = []migration.HistoryRecovery{{Owner: owner, IdentitySHA256: proof.IdentitySHA256, CaptureDigest: proof.CaptureDigest, ProofDigest: proof.Digest, SourceNode: node, Messages: h.Messages, HistorySHA256: h.SHA256}}
							}
						}
						return nil
					}))
					require.Len(t, p.History.Recoveries, 1)
				}
				require.NoError(t, strict.Close())
				w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "workspace"), p.Digest(), 128<<20)
				require.NoError(t, err)
				defer w.Close()
				prepared, err := migration.Prepare(ctx, p, w, r, r, nil)
				require.NoError(t, err)
				require.Equal(t, "prepared", prepared.Status)
				require.NotNil(t, prepared.Selection.HistoryPrefixes)
				require.EqualValues(t, 1, prepared.Selection.HistoryPrefixes.Accepted)
				require.Zero(t, prepared.Selection.HistoryPrefixes.Unresolved)
				if recovery {
					require.EqualValues(t, 1, prepared.Selection.HistoryPrefixes.Recovered)
				}
				archive, err := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
				require.NoError(t, err)
				_, err = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: p.Digest(), SourceCommit: p.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
				require.NoError(t, err)
				for _, n := range p.Sources {
					require.Equal(t, before[n.NodeID], fileDigests(t, n.DataDir))
					require.NoError(t, os.Rename(n.DataDir, n.DataDir+"-unmounted"))
				}
				fresh, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "fresh"), p.Digest(), 128<<20)
				require.NoError(t, err)
				defer fresh.Close()
				rebuilt, err := migration.PrepareArchive(ctx, p, fresh, r, archive)
				require.NoError(t, err)
				require.Equal(t, prepared.Selection, rebuilt.Selection)
				require.Equal(t, prepared.Conversion, rebuilt.Conversion)
				for retry := 0; retry < 2; retry++ {
					require.NoError(t, migrationv3.Install(ctx, p.Target, rebuilt.Conversion, fresh))
				}
				verified, err := migration.VerifyTargets(ctx, p.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
				require.NoError(t, err)
				require.Equal(t, "offline_verified", verified.Status)
			})
		}
	}

}

func TestHistoryCommandIdentityDoesNotAuthorizeEmptyChannelImport(t *testing.T) {
	r := migrationv2.Reader{}
	dir := compatibleMessageFixture(t)
	addEmptyChannelPair(t, dir, 1, "")
	count := 0
	_, err := r.ReadAuthorityCommands(context.Background(), migration.NodeOptions{NodeID: 1, Options: migration.Options{DataDir: dir, ShardCount: 2}}, func(migration.Row) error { return nil }, nil, func(raw migration.RawConfigCommand) error {
		_, empty, e := r.DecodeEmptyChannelCommand(raw)
		if e != nil {
			return e
		}
		if !empty {
			return nil
		}
		count++
		require.NotEmpty(t, r.DecodeAuthorityCommand(raw).DecodeErrorSHA256)
		require.Empty(t, r.DecodeHistoryConfigCommand(raw).DecodeErrorSHA256)
		broken := raw
		broken.Data = append(append([]byte(nil), raw.Data...), 0)
		require.NotEmpty(t, r.DecodeHistoryConfigCommand(broken).DecodeErrorSHA256)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
