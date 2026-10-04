//go:build e2e

package original_v2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestOriginalV2UnsupportedHistoryBlocksSingleNodeTarget(t *testing.T) {
	runOriginalV2(t, 1, 1, false)
}
func TestOriginalV2UnsupportedHistoryBlocksThreeNodeTarget(t *testing.T) {
	runOriginalV2(t, 1, 3, false)
}

func TestOriginalThreeNodeUnappliedHistoryBlocksSingleNodeTarget(t *testing.T) {
	runOriginalV2(t, 3, 1, false)
}
func TestOriginalThreeNodeUnappliedHistoryBlocksThreeNodeTarget(t *testing.T) {
	runOriginalV2(t, 3, 3, false)
}

func TestOriginalThreeNodeUnappliedHistoryBlocksFiveNodeTarget(t *testing.T) {
	runOriginalV2(t, 3, 5, false)
}

func TestOriginalEmptyGroupRemainsInvisibleUntilFirstMessage(t *testing.T) {
	runOriginalV2(t, 1, 1, true)
}

func runOriginalV2(t *testing.T, sourceCount, nodeCount int, emptyGroup bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	root := t.TempDir()
	var sources []any
	for i := 1; i <= sourceCount; i++ {
		name := "original-v2-server.tar.gz"
		if emptyGroup {
			name = "original-v2-empty.tar.gz"
		}
		if sourceCount > 1 {
			name = fmt.Sprintf("original-v2-unconverged-%d.tar.gz", i*11)
		}
		source := suite.UnpackMigrationFixture(t, name)
		nodeID := i
		if sourceCount > 1 {
			nodeID = i * 11
		}
		sources = append(sources, map[string]any{"node_id": nodeID, "data_dir": source, "shard_count": 2})
	}
	s := suite.New(t)
	var specs []suite.NodeSpec
	var targets []any
	for i := 1; i <= nodeCount; i++ {
		ports := suite.ReserveLoopbackPorts(t)
		nodeRoot := filepath.Join(root, fmt.Sprintf("node%d", i))
		require.NoError(t, os.Mkdir(nodeRoot, 0700))
		node := suite.NodeSpec{ID: uint64(i), Name: fmt.Sprintf("migrated-%d", i), RootDir: nodeRoot, DataDir: filepath.Join(nodeRoot, "data"), ConfigPath: filepath.Join(nodeRoot, "wukongim.toml"), StdoutPath: filepath.Join(nodeRoot, "stdout.log"), StderrPath: filepath.Join(nodeRoot, "stderr.log"), ClusterAddr: ports.ClusterAddr, GatewayAddr: ports.GatewayAddr, APIAddr: ports.APIAddr, ManagerAddr: ports.ManagerAddr}
		node.ConfigOverrides = map[string]string{"WK_CLUSTER_ID": "migration-e2e", "WK_CLUSTER_INITIAL_SLOT_COUNT": "4", "WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_SLOT_REPLICA_N": fmt.Sprint(nodeCount), "WK_CLUSTER_CHANNEL_REPLICA_N": fmt.Sprint(nodeCount), "WK_GATEWAY_TOKEN_AUTH_ON": "true"}
		if nodeCount == 1 {
			node.ConfigOverrides["WK_CLUSTER_NODES"] = fmt.Sprintf(`[{"id":1,"addr":%q}]`, ports.ClusterAddr)
		}
		specs = append(specs, node)
		targets = append(targets, map[string]any{"node_id": i, "addr": ports.ClusterAddr, "data_dir": node.DataDir})
	}
	plan := map[string]any{"version": 1, "source_commit": "a888f89533d0e7d1b2030e06504ca97f1ad891d4", "sources": sources, "target": map[string]any{"cluster_id": "migration-e2e", "created_at": "2026-09-06T06:56:42Z", "slot_count": 4, "hash_slot_count": 256, "replicas": nodeCount, "channel_replicas": nodeCount, "nodes": targets}}

	data, err := json.Marshal(plan)
	require.NoError(t, err)
	planPath := filepath.Join(root, "plan.json")
	require.NoError(t, os.WriteFile(planPath, data, 0600))
	cli := suite.BuildMigrationCLI(t)
	args := []string{"--plan", planPath, "--workspace", filepath.Join(root, "scratch")}
	if !emptyGroup {
		output := suite.RunMigrationCLIExpectFailure(t, ctx, cli, append([]string{"prepare"}, args...)...)
		if sourceCount == 1 {
			require.Contains(t, string(output), "incompatible with existing v3")
		} else {
			require.Contains(t, string(output), "unapplied")
		}
		for _, node := range specs {
			require.NoDirExists(t, node.DataDir)
		}
		return
	}
	suite.RunMigrationCLI(t, ctx, cli, append([]string{"prepare"}, args...)...)
	args = append(args, "--archive", filepath.Join(root, "archive"))
	for _, verb := range []string{"export", "import", "verify"} {
		suite.RunMigrationCLI(t, ctx, cli, append([]string{verb}, args...)...)
	}
	cluster := s.StartPreparedCluster(specs)
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	started := cluster.MustNode(1)
	if emptyGroup {
		var conversations []json.RawMessage
		_, err = suite.PostJSON(ctx, "http://"+started.APIAddr()+"/conversation/sync", map[string]any{"uid": "emptyalice", "msg_count": 10}, &conversations)
		require.NoError(t, err)
		require.Empty(t, conversations, "an original stored update timestamp must not activate an empty group")
		client, err := wkclient.New(wkclient.Config{Addr: started.GatewayAddr(), OperationTimeout: 5 * time.Second})
		require.NoError(t, err)
		defer client.Close()
		_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: "emptyalice", DeviceID: "empty-group-check", DeviceFlag: frame.DeviceFlag(1), Token: "synthetic-emptyalice"})
		require.NoError(t, err)
		sent, err := client.Send(ctx, wkclient.Message{ClientSeq: 1, ClientMsgNo: "first-after-migration", ChannelID: "emptygroup", ChannelType: 2, Payload: []byte("first message")})
		require.NoError(t, err)
		require.Equal(t, frame.ReasonSuccess, sent.ReasonCode, "the original subscriber must retain send permission")
		require.Equal(t, uint64(1), sent.MessageSeq)
		_, err = suite.PostJSON(ctx, "http://"+started.APIAddr()+"/conversation/sync", map[string]any{"uid": "emptyalice", "msg_count": 10}, &conversations)
		require.NoError(t, err)
		require.Len(t, conversations, 1)
		return
	}
}
