//go:build e2e

package bench_churn

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestIdentitySwapKeepsReplacementUserInGroup(t *testing.T) {
	cluster := startAuthenticatedBenchCluster(t)
	node := cluster.MustNode(1)

	scenario := model.Scenario{
		Version: "wkbench/v1",
		Run: model.RunConfig{
			ID: "e2e-bench-churn", Duration: 2 * time.Second, Cooldown: 5 * time.Second, FailFast: true,
		},
		Identity: model.IdentityConfig{
			TotalUsers: 4, UIDPrefix: "churn-u", DevicePrefix: "churn-d", ClientMsgPrefix: "churn-msg",
			Token: model.TokenConfig{Mode: "bench_api"},
		},
		Online: model.OnlineConfig{
			TotalUsers: 2, ConnectRate: model.Rate{PerSecond: 100}, GatewayBalance: "round_robin",
			Churn: model.ChurnConfig{
				Enabled: true, Interval: time.Second, Ratio: 0.5, SameUserRatio: 0, IdentitySwapRatio: 1,
			},
		},
		Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
			Name: "group-a", ChannelType: model.ChannelTypeGroup, Count: 1,
			Members: model.MembersConfig{Count: 2, Pick: "deterministic_hash", Overlap: "disallowed"},
			Online:  model.ChannelOnlineConfig{MemberRatio: 1},
			Shard:   model.ShardConfig{Mode: "hash"},
			Prepare: model.ChannelPrepareConfig{SubscribersBatchSize: 100},
		}}},
		Messages: model.MessagesConfig{
			Payload: model.PayloadConfig{SizeBytes: 32, Mode: "deterministic"},
			Traffic: []model.TrafficConfig{{
				Name: "group-send", ChannelRef: "group-a", RatePerChannel: model.Rate{PerSecond: 10},
				Concurrency: 4, AckTimeout: 3 * time.Second, SenderPick: "round_robin",
				Verify: model.VerifyConfig{Recv: model.RecvVerifyConfig{Mode: "none"}},
			}},
		},
	}
	workerConfig := model.Worker{ID: "worker-a", Weight: 1}
	plan, err := planner.Build(scenario, []model.Worker{workerConfig})
	require.NoError(t, err)
	assignment := worker.Assignment{
		RunID: scenario.Run.ID, WorkerID: workerConfig.ID,
		Target: model.Target{
			API:      model.TargetAPIConfig{Addrs: []string{"http://" + node.APIAddr()}},
			BenchAPI: model.BenchAPIConfig{Enabled: true, Addrs: []string{"http://" + node.APIAddr()}},
			Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{node.GatewayAddr()}}},
		},
		Scenario: scenario,
		Plan:     plan.Workers[workerConfig.ID],
	}

	runner := worker.NewDefaultWorkloadRunner(nil)
	runner.(worker.AssignmentStarter).BeginAssignment(assignment)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, runner.Prepare(ctx, assignment))
	require.NoError(t, runner.Connect(ctx, assignment))
	require.NoError(t, runner.Run(ctx, assignment))
	require.Equal(t, uint64(1), runner.(worker.MetricsReporter).MetricsSnapshot().Counters["churn_window_total"])
	require.NoError(t, runner.Cooldown(ctx, assignment))
}

// startAuthenticatedBenchCluster leaves CONNECT validation to real prepared
// sessions after all three Product HTTP surfaces report readiness.
func startAuthenticatedBenchCluster(t *testing.T) *suite.StartedCluster {
	t.Helper()
	var options []suite.Option
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(options, suite.WithNodeConfigOverrides(nodeID, map[string]string{
			"WK_BENCH_API_ENABLE": "true", "WK_BENCH_API_MAX_BATCH_SIZE": "100",
			"WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_CLUSTER_HASH_SLOT_COUNT": "256",
			"WK_CLUSTER_SLOT_REPLICA_N": "2",
		}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(options...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	return cluster
}

func TestBenchTokensAuthenticateAcrossNodes(t *testing.T) {
	cluster := startAuthenticatedBenchCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	api := target.NewClient(target.Config{APIAddrs: []string{"http://" + cluster.MustNode(1).APIAddr()}})
	const uid = "e2e-bench-token-user"
	previousToken := "wrong-token"
	for index, token := range []string{"e2e-bench-connect-token", "e2e-bench-rotated-token"} {
		require.NoError(t, api.UpsertTokens(ctx, model.BatchTokensRequest{
			RunID: "e2e-bench-token", BatchID: "prepare-" + strconv.Itoa(index), Upsert: true,
			Users: []model.UserTokenItem{{UID: uid, Token: token}},
		}))
		for _, nodeID := range []uint64{1, 2, 3} {
			connect := func(credential string) error {
				conn, err := client.New(client.Config{Addr: cluster.MustNode(nodeID).GatewayAddr(), OperationTimeout: 5 * time.Second})
				if err != nil {
					return err
				}
				defer conn.Close()
				_, err = conn.Connect(ctx, client.ConnectOptions{UID: uid, DeviceID: "e2e-bench-device", Token: credential})
				return err
			}
			require.NoError(t, connect(token), "persisted token must authenticate immediately on every ingress, including a non-replica")
			require.EqualError(t, connect(previousToken), "client: connack reason=ReasonAuthFail")
		}
		previousToken = token
	}
}
