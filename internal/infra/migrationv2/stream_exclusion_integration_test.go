//go:build integration

package migrationv2_test

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

// The native allocator, quorum commit, cold read and full restart must all use
// the compacted tail, including when exclusion left no message log to import.
func TestStreamExclusionNativeClusterAllocatesStrictSequencesAfterRestart(t *testing.T) {
	for _, count := range []int{1, 3} {
		for _, all := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d_node_cluster_all_streams_%t", count, all), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				p := diagnosticPlan(t, streamExclusionFixture(t, all))
				p.Messages = &migration.MessagePolicy{ExcludeCMD: true, ExcludeStreams: true, CompactSequences: true}
				p.Target.Replicas = uint16(count)
				p.Target.ChannelReplicas = uint16(count)
				p.Target.Nodes = nil
				voters := []cluster.ControlVoter{}
				for i := 0; i < count; i++ {
					l, err := net.Listen("tcp", "127.0.0.1:0")
					require.NoError(t, err)
					addr := l.Addr().String()
					require.NoError(t, l.Close())
					id := uint64(101 + i)
					p.Target.Nodes = append(p.Target.Nodes, migration.TargetNode{NodeID: id, Addr: addr, DataDir: filepath.Join(t.TempDir(), "node")})
					voters = append(voters, cluster.ControlVoter{NodeID: id, Addr: addr})
				}
				w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
				require.NoError(t, err)
				defer w.Close()
				r := migrationv2.Reader{}
				prepared, err := migration.Prepare(ctx, p, w, r, r, nil)
				require.NoError(t, err)
				require.NoError(t, migrationv3.Install(ctx, p.Target, prepared.Conversion, w))
				_, err = migration.VerifyTargets(ctx, p.Target, prepared.Selection, w, r, migrationv3.Inspector{})
				require.NoError(t, err)
				retained := 0
				if !all {
					retained = 1
				}
				id := ch.ChannelID{ID: "migrationgroup", Type: 2}
				for run := 0; run < 3; run++ {
					func() {
						nodes := make([]*cluster.Node, count)
						for i, n := range p.Target.Nodes {
							node, err := cluster.New(cluster.Config{NodeID: n.NodeID, ListenAddr: n.Addr, DataDir: n.DataDir, Control: cluster.ControlConfig{ClusterID: p.Target.ClusterID, Voters: voters}, Slots: cluster.SlotConfig{InitialSlotCount: p.Target.SlotCount, HashSlotCount: 256, ReplicaCount: uint16(count)}, Channel: cluster.ChannelConfig{ReplicaCount: uint16(count)}})
							require.NoError(t, err)
							nodes[i] = node
						}
						defer func() {
							var wg sync.WaitGroup
							for _, node := range nodes {
								wg.Add(1)
								go func(n *cluster.Node) { defer wg.Done(); _ = n.Stop(context.Background()) }(node)
							}
							wg.Wait()
						}()
						var wg sync.WaitGroup
						errs := make(chan error, count)
						for _, node := range nodes {
							wg.Add(1)
							go func(n *cluster.Node) { defer wg.Done(); errs <- n.Start(ctx) }(node)
						}
						wg.Wait()
						close(errs)
						for err := range errs {
							require.NoError(t, err)
						}
						check := func(want int) {
							for _, node := range nodes {
								var read store.ReadCommittedResult
								var readErr error
								defer func() {
									if t.Failed() {
										t.Logf("last cold read error: %T %v", readErr, readErr)
									}
								}()

								require.Eventually(t, func() bool {
									rows, err := node.ReadChannelCommittedBatch(ctx, []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20}}})
									readErr = err
									if err != nil || len(rows) != 1 {
										return false
									}
									read, readErr = rows[0].Read, rows[0].Err
									// A channel without any retained history has no runtime
									// metadata yet. Native cluster reads report ChannelNotFound;
									// the first native SEND creates it. This is not an HTTP
									// empty-history response acceptance test.
									if want == 0 && ch.ErrorMatches(readErr, ch.ErrChannelNotFound) {
										return len(read.Messages) == 0
									}
									return readErr == nil && len(read.Messages) == want
								}, 20*time.Second, 50*time.Millisecond, "cold read run=%d want=%d last=%+v error=%v", run, want, &read, &readErr)
								for i, m := range read.Messages {
									require.EqualValues(t, i+1, m.MessageSeq)
									if i < retained {
										require.EqualValues(t, 2096462572977917952, m.MessageID)
										require.Equal(t, []byte("消息1"), m.Payload)
									} else {
										require.EqualValues(t, 2100000000000000000+i-retained, m.MessageID)
										require.Equal(t, []byte(fmt.Sprintf("after-migration-%d", i-retained)), m.Payload)
									}
								}
							}
						}
						check(retained + run)
						if run == 2 {
							return
						} // A second complete restart verifies the last ACK as well.
						req := ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: uint64(2100000000000000000 + run), FromUID: "new-sender", ClientMsgNo: fmt.Sprintf("new-client-%d", run), ServerTimestampMS: 1788670605000 + int64(run), Payload: []byte(fmt.Sprintf("after-migration-%d", run))}}
						var seq uint64
						require.Eventually(t, func() bool {
							callCtx, done := context.WithTimeout(ctx, 2*time.Second)
							defer done()
							ack, err := nodes[run%count].AppendChannel(callCtx, req)
							if err != nil {
								return false
							}
							seq = ack.MessageSeq
							return true
						}, 20*time.Second, 50*time.Millisecond)
						require.EqualValues(t, retained+run+1, seq)
						check(retained + run + 1)
					}()
				}
			})
		}
	}
}
