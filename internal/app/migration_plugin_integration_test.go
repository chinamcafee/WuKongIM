//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	accessplugin "github.com/WuKongIM/WuKongIM/internal/access/plugin"
	"github.com/WuKongIM/WuKongIM/internal/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
	"github.com/stretchr/testify/require"
)

// TestMigrationOriginalPluginProtocol runs only the exact audited executable in
// an operator-provided isolated environment. Replies stop at a recording port;
// this certifies protocol/config handoff, not cluster writes or cutover readiness.
func TestMigrationOriginalPluginProtocol(t *testing.T) {
	path := os.Getenv("WKMIGRATE_TEST_PLUGIN_BINARY")
	if path == "" {
		t.Skip("requires audited original plugin executable")
	}
	binary, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "671b3436d1a8d765371077009b1dfd6dec4528a1ce9cdc0dbebe2cfddc5b3224", fmt.Sprintf("%x", sha256.Sum256(binary)))
	for node := uint64(1); node <= 3; node++ {
		t.Run(fmt.Sprint(node), func(t *testing.T) {
			root, err := os.MkdirTemp("", "wkpm-")
			require.NoError(t, err)
			defer os.RemoveAll(root)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			const no = "wk.plugin.ai-example"
			name := fmt.Sprintf("migration-node-%d", node)
			store := pluginhost.NewStore(filepath.Join(root, "state"))
			config, err := json.Marshal(map[string]string{"name": name})
			require.NoError(t, err)
			desired := pluginhost.DesiredState{No: no, Config: config, Enabled: true, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0)}
			if assigned := os.Getenv("WKMIGRATE_TEST_PLUGIN_STATE_ROOT"); assigned != "" {
				desired, err = pluginhost.NewStore(filepath.Join(assigned, fmt.Sprint(node))).Load(no)
				require.NoError(t, err)
				var fields map[string]string
				require.NoError(t, json.Unmarshal(desired.Config, &fields))
				name = fields["name"]
				require.True(t, desired.Enabled, "fixture requires enabled audited plugin")
			}
			require.NoError(t, store.Save(desired))
			socket := pluginhost.NewSocketServer(filepath.Join(root, "p.sock"))
			invoker := pluginhost.NewInvoker(socket, pluginhost.WithTimeout(2*time.Second))
			sandbox := filepath.Join(root, "sandbox")
			runtime := pluginhost.NewRuntime(pluginhost.RuntimeOptions{Enable: true, Dir: root, SocketPath: filepath.Join(root, "p.sock"), SandboxDir: sandbox, StateDir: filepath.Join(root, "state"), Store: store, Socket: socket, Invoker: invoker, Timeout: time.Second, Scanner: func(string) ([]pluginhost.ProcessSpec, error) {
				return []pluginhost.ProcessSpec{{No: no, Path: path}}, nil
			}})
			sender := &migrationPluginReplyRecorder{replies: make(chan message.SendCommand, 8)}
			app, err := pluginusecase.NewApp(pluginusecase.Options{NodeID: node, Runtime: pluginRuntimeAdapter{runtime: runtime, sandboxDir: sandbox}, Invoker: invoker, DesiredStore: pluginDesiredStoreAdapter{store: store}, Messages: sender, ReceiveBindings: migrationPluginTestBinding{}})
			require.NoError(t, err)
			_, err = accessplugin.NewServer(accessplugin.Options{Routes: socket, Usecase: app, Timeout: 2 * time.Second})
			require.NoError(t, err)
			require.NoError(t, runtime.Start(ctx))
			defer func() {
				stop, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				require.NoError(t, runtime.Stop(stop))
			}()
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				p, ok := runtime.Registry().Get(no)
				if ok && p.Status == pluginhost.StatusRunning && len(p.Methods) > 0 {
					require.Equal(t, "0.0.1", p.Version)
					require.Equal(t, 1, p.Priority)
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("plugin registration timed out")
				case <-ticker.C:
				}
			}
			for _, channelType := range []uint8{1, 2} {
				channel := "migration-group"
				if channelType == 1 {
					channel = "alice@robot"
				}
				event := pluginevents.ReceiveOffline{UID: "robot", FromUID: "alice", ChannelID: channel, ChannelType: channelType, MessageID: uint64(10 + channelType), MessageSeq: 1, Payload: []byte(`{"type":1,"content":"hello"}`)}
				require.NoError(t, app.ReceiveOffline(ctx, event))
				select {
				case reply := <-sender.replies:
					require.Equal(t, "robot", reply.FromUID)
					require.EqualValues(t, channelType, reply.ChannelType)
					if channelType == 1 {
						require.Equal(t, "alice", reply.ChannelID)
					} else {
						require.Equal(t, channel, reply.ChannelID)
					}
					var payload map[string]any
					require.NoError(t, json.Unmarshal(reply.Payload, &payload))
					require.True(t, fmt.Sprintf("我是%s,收到您的消息：hello", name) == payload["content"], "reply does not reflect assigned config; values intentionally omitted")
				case <-ctx.Done():
					t.Fatal("plugin reply timed out")
				}
			}
			t.Log("audited executable startup config and personal/group replies passed; send recording port only")
		})
	}
}

type migrationPluginReplyRecorder struct{ replies chan message.SendCommand }

func (r *migrationPluginReplyRecorder) Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error) {
	select {
	case r.replies <- cmd:
		return message.SendResult{}, nil
	case <-ctx.Done():
		return message.SendResult{}, ctx.Err()
	}
}

type migrationPluginTestBinding struct{}

func (migrationPluginTestBinding) ListPluginBindingsByUID(_ context.Context, uid string) ([]pluginusecase.PluginBinding, error) {
	if uid != "robot" {
		return nil, nil
	}
	return []pluginusecase.PluginBinding{{UID: uid, PluginNo: "wk.plugin.ai-example"}}, nil
}
