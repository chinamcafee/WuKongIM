//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

// migrationPluginComponentFixture deliberately contains only the captured
// PluginUser range. Its digest is not a full-source preparation certificate.
type migrationPluginComponentFixture struct {
	ParentCaptureDigest string
	Nodes               []migration.NodeSnapshot
	Rows                []transfer.SpoolRow
}

// TestMigrationPluginBindingsInNativeThreeNodeCluster joins original captured
// bindings, assigned node-local configuration and the exact original executable
// with native cluster routing and ordinary durable message sends. It never
// relaxes the full-source plugin compatibility gate.
func TestMigrationPluginBindingsInNativeThreeNodeCluster(t *testing.T) {
	testMigrationPluginBindingsAndRecovery(t, 3)
}

func TestMigrationPluginBindingsInNativeSingleNodeCluster(t *testing.T) {
	testMigrationPluginBindingsAndRecovery(t, 1)
}

func testMigrationPluginBindingsAndRecovery(t *testing.T, nodeCount int) {
	t.Helper()
	program, fixturePath, assigned := os.Getenv("WKMIGRATE_TEST_PLUGIN_BINARY"), os.Getenv("WKMIGRATE_TEST_PLUGIN_BINDING_FIXTURE"), os.Getenv("WKMIGRATE_TEST_PLUGIN_STATE_ROOT")
	if program == "" || fixturePath == "" || assigned == "" {
		t.Skip("requires audited program, captured binding fixture and assigned configuration")
	}
	configPolicy := os.Getenv("WKMIGRATE_TEST_PLUGIN_CONFIG_POLICY")
	require.True(t, configPolicy == "" || configPolicy == "source-1001", "unsupported isolated configuration candidate")
	programBytes, err := os.ReadFile(program)
	require.NoError(t, err)
	require.True(t, fmt.Sprintf("%x", sha256.Sum256(programBytes)) == "671b3436d1a8d765371077009b1dfd6dec4528a1ce9cdc0dbebe2cfddc5b3224", "unexpected plugin executable")
	fixtureBytes, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	var fixture migrationPluginComponentFixture
	require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
	require.Equal(t, "3780b1757dbba3b6e46bf2c750bcaec1c09d7b65d974f586d365e2f893d5a896", fixture.ParentCaptureDigest)
	require.Len(t, fixture.Nodes, 3)
	require.Len(t, fixture.Rows, 9)
	root, err := os.MkdirTemp("", "wkpc-")
	require.NoError(t, err)
	defer os.RemoveAll(root)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	digest := fmt.Sprintf("%x", sha256.Sum256(fixtureBytes))
	w, err := transfer.OpenSpool(filepath.Join(root, "spool"), digest, 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	for _, record := range fixture.Rows {
		var row migration.Row
		require.NoError(t, json.Unmarshal(record.Value, &row))
		require.Equal(t, "PluginUser", row.Table)
		if row.Kind == migration.Primary {
			fields, err := json.Marshal(row.Fields)
			require.NoError(t, err)
			require.True(t, fmt.Sprintf("%x", sha256.Sum256(fields)) == "62ddf9a60db1d99a6c79844ef59b426e94a226295ff32e7cab3d9678d45d4967", "binding differs from audited source")
		}
	}
	require.NoError(t, w.Put(ctx, fixture.Rows))
	// Only this binding subset is selected and installed. Original snapshots
	// still enforce source topology, Slot progress and formal replica agreement.
	capture := migration.SourceCapture{Nodes: fixture.Nodes, Digest: digest, Tables: map[string]uint64{"PluginUser": 3}}
	var sources []migration.NodeOptions
	for _, node := range fixture.Nodes {
		sources = append(sources, migration.NodeOptions{NodeID: node.NodeID, Options: migration.Options{ShardCount: 8}})
	}
	catalog, err := migration.BuildSourceCatalog(ctx, capture, w, r)
	require.NoError(t, err)
	require.NoError(t, migration.ValidateSourceIndexes(ctx, capture, sources, w, r))
	selected, err := migration.SelectSources(ctx, capture, catalog, w, r, nil)
	require.NoError(t, err)
	converted, err := migration.BuildTargetRecords(ctx, selected, w, r)
	require.NoError(t, err)
	require.Equal(t, uint64(1), converted.Metadata["plugin_binding"])
	var binding *migration.SourcePluginBinding
	require.NoError(t, migration.WalkSelectedSources(ctx, w, func(record migration.SelectedRecord) error {
		facts, err := r.DecodeBusiness(record.Row, record.Identity)
		if err != nil {
			return err
		}
		if facts.PluginBinding != nil {
			binding = facts.PluginBinding
		}
		return nil
	}))
	require.NotNil(t, binding)
	const no = "wk.plugin.ai-example"
	require.True(t, binding.PluginNo == no, "unexpected bound plugin")
	plan := migration.TargetPlan{ClusterID: "migration-plugin-component", CreatedAt: time.Unix(1788670602, 0).UTC(), SlotCount: 4, HashSlotCount: 256, Replicas: uint16(nodeCount), ChannelReplicas: uint16(nodeCount)}
	var voters []cluster.ControlVoter
	for n := uint64(1); n <= uint64(nodeCount); n++ {
		addr := freeSendackSmokeTCPAddr(t)
		plan.Nodes = append(plan.Nodes, migration.TargetNode{NodeID: n, Addr: addr, DataDir: filepath.Join(root, fmt.Sprintf("node%d", n))})
		voters = append(voters, cluster.ControlVoter{NodeID: n, Addr: addr})
	}
	require.NoError(t, migrationv3.Install(ctx, plan, converted, w))
	verified, err := migration.VerifyTargets(ctx, plan, selected, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	require.Equal(t, uint64(nodeCount), verified.Metadata["plugin_binding"])
	require.False(t, verified.CutoverReady)
	configs := make([]Config, nodeCount)
	names := make([]string, nodeCount)
	configHashes := []string{"7cae637b161fc5057487e38b8bc39277b683cb1c3c6b8c366a2a21a97c47616d", "31db7bfdd25df908d897fbc4aaf613575a550c0ae4333826f38b00b096d038a1", "ff243a693d2a9e1d4fb0235c40a8879fc19a860ce52d138d7a2e05c1fbded69b"}
	if configPolicy == "source-1001" {
		// The user approved source-1001 config for this exact plugin. This
		// component still cannot authorize full-source preparation.
		for i := range configHashes {
			configHashes[i] = configHashes[0]
		}
	}
	for i, n := range plan.Nodes {
		pluginRoot := filepath.Join(root, fmt.Sprintf("p%d", n.NodeID))
		require.NoError(t, os.MkdirAll(pluginRoot, 0700))
		// The native scanner derives plugin identity from the filename. Preserve
		// the executable bytes while dropping the old platform filename suffix.
		require.NoError(t, os.WriteFile(filepath.Join(pluginRoot, no+".wkp"), programBytes, 0500))
		desired, err := pluginhost.NewStore(filepath.Join(assigned, fmt.Sprint(n.NodeID))).Load(no)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(desired.Config, &fields))
		canonical, err := json.Marshal(fields)
		require.NoError(t, err)
		require.True(t, fmt.Sprintf("%x", sha256.Sum256(canonical)) == configHashes[i], "assigned configuration differs from audit")
		require.NoError(t, json.Unmarshal(fields["name"], &names[i]))
		require.True(t, desired.Enabled)
		stateDir := filepath.Join(pluginRoot, "state")
		require.NoError(t, pluginhost.NewStore(stateDir).Save(desired))
		cfg := Config{NodeID: n.NodeID, DataDir: n.DataDir, Cluster: cluster.Config{NodeID: n.NodeID, ListenAddr: n.Addr, DataDir: n.DataDir, Control: cluster.ControlConfig{ClusterID: plan.ClusterID, Voters: voters}, Slots: cluster.SlotConfig{InitialSlotCount: 4, HashSlotCount: 256, ReplicaCount: uint16(nodeCount)}, Channel: cluster.ChannelConfig{ReplicaCount: uint16(nodeCount)}}, Plugin: PluginConfig{Enable: true, Dir: pluginRoot, StateDir: stateDir, SocketPath: filepath.Join(pluginRoot, "p.sock"), SandboxDir: filepath.Join(pluginRoot, "sandbox"), Timeout: 3 * time.Second}}
		cfg.Plugin.SetEnableExplicit(true)
		cfg.Plugin.SetExplicitFlags(true)
		// Product configuration enables delivery. A zero-value app test config
		// would omit the post-commit path that owns offline plugin invocation.
		cfg.Delivery = DeliveryConfig{Enabled: true, RecipientWorkerConcurrency: 4}
		configs[i] = cfg
	}
	type history struct {
		channel ch.ChannelID
		rows    store.ReadCommittedResult
	}
	var histories []history
	remember := func(node *cluster.Node, channel ch.ChannelID) {
		rows, err := node.ReadChannelCommitted(ctx, channel, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
		require.NoError(t, err)
		require.NotEmpty(t, rows.Messages)
		histories = append(histories, history{channel, rows})
	}
	// The final round only reads existing histories: no new append may warm a
	// channel before its complete pre-restart plugin reply is checked.
	for round := 0; round < 3; round++ {
		apps := make([]*App, nodeCount)
		func() {
			defer func() {
				errs := make(chan error, nodeCount)
				for _, a := range apps {
					go func(a *App) {
						if a == nil {
							errs <- nil
							return
						}
						stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						errs <- a.Stop(stop)
					}(a)
				}
				for range apps {
					require.NoError(t, <-errs)
				}
			}()
			for i, cfg := range configs {
				apps[i], err = New(cfg, WithLogger(wklog.NewNop()), WithGateway(nil))
				require.NoError(t, err)
			}
			errs := make(chan error, nodeCount)
			for _, a := range apps {
				require.NotNil(t, a.onlineDelivery, "automatic Receive requires the product delivery runtime")
				go func(a *App) { errs <- a.Start(ctx) }(a)
			}
			for range apps {
				require.NoError(t, <-errs)
			}
			for _, a := range apps {
				require.Eventually(t, func() bool {
					p, ok := a.pluginRuntime.(*pluginhost.Runtime).Registry().Get(no)
					if !ok || p.Status != pluginhost.StatusRunning {
						return false
					}
					rows, e := a.cluster.(*cluster.Node).ListPluginBindingsByUID(ctx, binding.UID)
					return e == nil && len(rows) == 1 && rows[0].PluginNo == no
				}, 15*time.Second, 50*time.Millisecond, "plugin registration or cluster binding lookup unavailable")
			}
			for _, h := range histories {
				for _, a := range apps {
					got, err := a.cluster.(*cluster.Node).ReadChannelCommitted(ctx, h.channel, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
					require.NoError(t, err)
					require.True(t, reflect.DeepEqual(h.rows.Messages, got.Messages), "pre-restart history changed; private fields omitted")
				}
			}
			t.Logf("round %d: %d complete pre-restart histories verified through %d nodes before any new send", round+1, len(histories), nodeCount)
			if round == 2 {
				return
			}
			// Invoke on each node to prove its assigned name is consumed while
			// binding reads and plugin-origin sends use real cluster adapters.
			for i, a := range apps {
				for _, kind := range []uint8{1, 2} {
					peer := fmt.Sprintf("migration-direct-%d-%d-%d", round, i, kind)
					id := ch.ChannelID{ID: peer, Type: kind}
					if kind == 1 {
						id.ID = channelid.EncodePersonChannel(peer, binding.UID)
					} else {
						require.NoError(t, a.channels.AddSubscribers(ctx, channelusecase.SubscriberCommand{ChannelID: id.ID, ChannelType: 2, Subscribers: []string{peer, binding.UID}}))
					}
					event := pluginevents.ReceiveOffline{UID: binding.UID, FromUID: peer, ChannelID: id.ID, ChannelType: kind, MessageID: uint64(1000 + round*100 + i*10 + int(kind)), MessageSeq: 1, Payload: []byte(`{"type":1,"content":"hello"}`)}
					require.NoError(t, a.plugins.ReceiveOffline(ctx, event))
					migrationPluginCommittedReply(t, ctx, a.cluster.(*cluster.Node), id, binding.UID, []string{names[i]}, 1)
					remember(a.cluster.(*cluster.Node), id)
				}
			}
			// Ordinary sends must trigger post-commit offline Receive without a
			// direct hook call. A bound reply must itself be durably committed.
			for i, a := range apps {
				for _, kind := range []uint8{1, 2} {
					peer := fmt.Sprintf("migration-send-%d-%d-%d", round, i, kind)
					id := ch.ChannelID{ID: peer, Type: kind}
					to := peer
					if kind == 1 {
						id.ID = channelid.EncodePersonChannel(peer, binding.UID)
						to = binding.UID
					} else {
						require.NoError(t, a.channels.AddSubscribers(ctx, channelusecase.SubscriberCommand{ChannelID: peer, ChannelType: 2, Subscribers: []string{peer, binding.UID}}))
					}
					result, err := a.messages.Send(ctx, message.SendCommand{FromUID: peer, ChannelID: to, ChannelType: kind, NormalizePersonChannel: true, ClientMsgNo: peer, Payload: []byte(`{"type":1,"content":"hello"}`)})
					require.NoError(t, err)
					require.Equal(t, message.ReasonSuccess, result.Reason)
					require.Equal(t, uint64(1), result.MessageSeq)
					configNode := migrationPluginCommittedReply(t, ctx, a.cluster.(*cluster.Node), id, binding.UID, names, 2)
					remember(a.cluster.(*cluster.Node), id)
					if configPolicy == "source-1001" {
						t.Logf("automatic reply: round=%d entry_node=%d channel_type=%d config_source=1001", round+1, i+1, kind)
					} else {
						t.Logf("automatic reply: round=%d entry_node=%d channel_type=%d config_node=%d", round+1, i+1, kind, configNode)
					}
				}
			}
		}()
		if round < 2 {
			t.Logf("round %d: %d nodes, %d direct hook replies and %d automatic post-commit replies durably committed", round+1, nodeCount, nodeCount*2, nodeCount*2)
		}
	}
}

func migrationPluginCommittedReply(t *testing.T, ctx context.Context, node *cluster.Node, id ch.ChannelID, uid string, names []string, count int) int {
	t.Helper()
	var rows store.ReadCommittedResult
	require.Eventually(t, func() bool {
		var err error
		rows, err = node.ReadChannelCommitted(ctx, id, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
		return err == nil && len(rows.Messages) >= count
	}, 10*time.Second, 50*time.Millisecond, "plugin reply was not committed")
	require.True(t, len(rows.Messages) == count, "unexpected committed message count; contents omitted")
	reply := rows.Messages[count-1]
	require.True(t, reply.FromUID == uid, "reply sender differs from bound UID")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(reply.Payload, &payload))
	matched := 0
	for i, name := range names {
		if payload["content"] == fmt.Sprintf("我是%s,收到您的消息：hello", name) {
			matched = i + 1
		}
	}
	require.Positive(t, matched, "reply differs from assigned configuration; values omitted")
	return matched
}
