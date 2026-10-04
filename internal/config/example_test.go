package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestRootTOMLExampleLoads(t *testing.T) {
	cfg, err := Load(Options{Args: []string{"-config", filepath.Join("..", "..", "wukongim.toml.example")}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(root example) error = %v", err)
	}
	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 {
		t.Fatalf("NodeID = %d/%d, want 1", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.Cluster.Slots.HashSlotCount != 256 {
		t.Fatalf("HashSlotCount = %d, want 256", cfg.Cluster.Slots.HashSlotCount)
	}
	if cfg.Cluster.Slots.InitialSlotCount != 12 {
		t.Fatalf("InitialSlotCount = %d, want 12", cfg.Cluster.Slots.InitialSlotCount)
	}
	assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
	if cfg.Cluster.Storage.CommitShards != 1 {
		t.Fatalf("CommitShards = %d, want 1", cfg.Cluster.Storage.CommitShards)
	}
}

func TestScriptThreeNodeClusterUsesQPSValidatedRPCDefaults(t *testing.T) {
	for node := 1; node <= 3; node++ {
		path := filepath.Join("..", "..", "scripts", "wukongim", fmt.Sprintf("wukongim-node%d.toml", node))
		cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
		if err != nil {
			t.Fatalf("Load(%s) error = %v", path, err)
		}
		assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
		if cfg.Cluster.Storage.CommitShards != 1 {
			t.Fatalf("%s CommitShards = %d, want 1", path, cfg.Cluster.Storage.CommitShards)
		}
	}
}

func TestCommandTOMLExampleLoads(t *testing.T) {
	cfg, err := Load(Options{Args: []string{"-config", filepath.Join("..", "..", "cmd", "wukongim", "wukongim.toml.example")}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(cmd example) error = %v", err)
	}
	if cfg.Cluster.Control.ClusterID != "wukongim-single" {
		t.Fatalf("ClusterID = %q, want wukongim-single", cfg.Cluster.Control.ClusterID)
	}
	if cfg.Cluster.Slots.InitialSlotCount != 12 || cfg.Cluster.Slots.HashSlotCount != 256 {
		t.Fatalf("cmd topology = logical Slot Groups %d / physical hash slots %d, want 12 / 256", cfg.Cluster.Slots.InitialSlotCount, cfg.Cluster.Slots.HashSlotCount)
	}
	assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
}

func TestScriptSingleNodeClusterUsesTwelveLogicalAndDefaultPhysicalSlots(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "wukongim", "wukongim.toml")
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(script example) error = %v", err)
	}
	if cfg.Cluster.Slots.InitialSlotCount != 12 || cfg.Cluster.Slots.HashSlotCount != 256 {
		t.Fatalf("script topology = logical Slot Groups %d / physical hash slots %d, want 12 / 256", cfg.Cluster.Slots.InitialSlotCount, cfg.Cluster.Slots.HashSlotCount)
	}
	assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
	if cfg.Cluster.Storage.CommitShards != 1 {
		t.Fatalf("%s CommitShards = %d, want 1", path, cfg.Cluster.Storage.CommitShards)
	}
}

func TestV2WKYAMLEquivalentTOMLLoads(t *testing.T) {
	path := filepath.Join("..", "..", "config", "wukongim-v3-single-node.toml")
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(%s) error = %v", path, err)
	}
	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 || cfg.DataDir != "./wukongimdata" {
		t.Fatalf("node config = id:%d/%d data:%q", cfg.NodeID, cfg.Cluster.NodeID, cfg.DataDir)
	}
	if len(cfg.Cluster.Control.Voters) != 1 || cfg.Cluster.Control.Voters[0].NodeID != 1 || !cfg.Cluster.Control.AllowBootstrap {
		t.Fatalf("control config = %#v", cfg.Cluster.Control)
	}
	if cfg.Cluster.Slots.ReplicaCount != 1 || cfg.Cluster.Channel.ReplicaCount != 1 || cfg.Cluster.Slots.HashSlotCount != 256 {
		t.Fatalf("replica config = slots:%d channels:%d hash_slots:%d", cfg.Cluster.Slots.ReplicaCount, cfg.Cluster.Channel.ReplicaCount, cfg.Cluster.Slots.HashSlotCount)
	}
	if cfg.API.ListenAddr != "127.0.0.1:5001" || cfg.API.ExternalTCPAddr != "192.168.10.110:5100" || cfg.API.ExternalWSAddr != "ws://192.168.10.110:5200" {
		t.Fatalf("API config = %#v", cfg.API)
	}
	if cfg.Manager.ListenAddr != "127.0.0.1:5300" || !cfg.Manager.AuthOn || len(cfg.Manager.Users) != 1 {
		t.Fatalf("manager config = %#v", cfg.Manager)
	}
	if len(cfg.Gateway.Listeners) != 2 || !cfg.Gateway.TokenAuthEnabled ||
		!cfg.Observability.MetricsEnabled || !cfg.Message.PersonWhitelistEnabled ||
		!cfg.Delivery.Enabled || !cfg.Plugin.Enable {
		t.Fatalf("runtime config = listeners:%d token_auth:%t metrics:%t whitelist:%t delivery:%t plugin:%t",
			len(cfg.Gateway.Listeners), cfg.Gateway.TokenAuthEnabled,
			cfg.Observability.MetricsEnabled, cfg.Message.PersonWhitelistEnabled,
			cfg.Delivery.Enabled, cfg.Plugin.Enable)
	}
	if !cfg.Webhook.Enabled || cfg.Webhook.HTTPAddr != "http://linku-im-processor:LinkU-WuKongIM-Webhook-2026@localhost/link-u-im-processor/webhook" ||
		!slices.Equal(cfg.Webhook.FocusEvents, []string{"msg.offline.v2", "msg.notify"}) ||
		cfg.Webhook.RetryMaxAttempts != 5 {
		t.Fatalf("webhook config = %#v", cfg.Webhook)
	}
	if cfg.Log.Level != "debug" || cfg.Log.Dir != "./wukongimdata/logs" {
		t.Fatalf("log config = %#v", cfg.Log)
	}
}

func TestSingleNodeClusterPrometheusExamplesUseDedicatedDefaultPort(t *testing.T) {
	files := []string{
		filepath.Join("..", "..", "wukongim.toml.example"),
		filepath.Join("..", "..", "cmd", "wukongim", "wukongim.toml.example"),
		filepath.Join("..", "..", "scripts", "wukongim", "wukongim.toml"),
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", file, err)
			}
			if !strings.Contains(string(content), `listen_addr = "127.0.0.1:9099"`) {
				t.Fatalf("%s must use the dedicated app-managed Prometheus port 9099", file)
			}
		})
	}
}

func TestGatewayExamplesUseQualifiedAsyncSendBatchLimit(t *testing.T) {
	files := []string{filepath.Join("..", "..", "wukongim.toml.example")}
	for _, pattern := range []string{
		filepath.Join("..", "..", "cmd", "wukongim", "*.toml.example"),
		filepath.Join("..", "..", "scripts", "wukongim", "*.toml"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("Glob(%s) error = %v", pattern, err)
		}
		files = append(files, matches...)
	}

	foundGateway := 0
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", file, err)
		}
		if !strings.Contains(string(content), "[gateway]") {
			continue
		}
		foundGateway++
		want := "# Maximum SEND frames coalesced into one asynchronous gateway dispatch batch.\n" +
			"# The 128-record limit is qualified for sustained high-QPS workloads.\n" +
			"default_session_async_send_batch_max_records = 128"
		if strings.Contains(filepath.ToSlash(file), "scripts/wukongim/wukongim-node") {
			want = "# Maximum SEND frames coalesced into one asynchronous gateway dispatch batch.\n" +
				"# The reviewed chat-lifecycle profile keeps one SEND per dispatch because each\n" +
				"# sender already allows only one in-flight SENDACK operation.\n" +
				"default_session_async_send_batch_max_records = 1"
		}
		if !strings.Contains(string(content), want) {
			t.Errorf("%s must document the qualified gateway async SEND batch limit", file)
		}
	}
	if foundGateway == 0 {
		t.Fatal("no shipped [gateway] examples found")
	}
}

func TestPresenceExamplesDocumentTouchMaxRoutesPerFlush(t *testing.T) {
	files := []string{filepath.Join("..", "..", "wukongim.toml.example")}
	for _, pattern := range []string{
		filepath.Join("..", "..", "cmd", "wukongim", "*.toml.example"),
		filepath.Join("..", "..", "scripts", "wukongim", "*.toml"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("Glob(%s) error = %v", pattern, err)
		}
		files = append(files, matches...)
	}

	want := "# Maximum owner-local dirty routes processed across all touch chunks in one flush.\n" +
		"# Must be positive and greater than or equal to touch_batch_size.\n" +
		"touch_max_routes_per_flush = 65536"
	foundPresence := 0
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", file, err)
		}
		if !strings.Contains(string(content), "[presence]") {
			continue
		}
		foundPresence++
		if !strings.Contains(string(content), want) {
			t.Errorf("%s must document touch_max_routes_per_flush with the required adjacent English comments", file)
		}
	}
	if foundPresence == 0 {
		t.Fatal("no shipped [presence] examples found")
	}
}

func TestMessageExamplesDocumentSystemUID(t *testing.T) {
	files := []string{
		filepath.Join("..", "..", "wukongim.toml.example"),
		filepath.Join("..", "..", "cmd", "wukongim", "wukongim.toml.example"),
	}
	want := "# System account UID used when a trusted message sender is omitted.\n" +
		"# Omitted or empty values use the built-in ____system account.\n" +
		"system_uid = \"____system\""
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", file, err)
		}
		if !strings.Contains(string(content), want) {
			t.Errorf("%s must document message.system_uid with the required adjacent English comments", file)
		}
	}
}

func TestDeliveryExamplesDocumentRecipientWorkerConcurrency(t *testing.T) {
	files := []string{filepath.Join("..", "..", "wukongim.toml.example")}
	for _, pattern := range []string{
		filepath.Join("..", "..", "cmd", "wukongim", "*.toml.example"),
		filepath.Join("..", "..", "scripts", "wukongim", "*.toml"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("Glob(%s) error = %v", pattern, err)
		}
		files = append(files, matches...)
	}

	want := "# Maximum number of distinct Channels with delivery plans executing concurrently on this node.\n" +
		"# Plans for one Channel stay FIFO; ready Channels share workers without hash-shard blocking.\n" +
		"# This is independent from channel_append.recipient_authority_dispatch_concurrency.\n" +
		"recipient_worker_concurrency = 320"
	foundDelivery := 0
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", file, err)
		}
		if !strings.Contains(string(content), "[delivery]") {
			continue
		}
		foundDelivery++
		if !strings.Contains(string(content), want) {
			t.Errorf("%s must document recipient_worker_concurrency with the required adjacent English comments", file)
		}
	}
	if foundDelivery == 0 {
		t.Fatal("no shipped [delivery] examples found")
	}
}

func TestDockerThreeNodeClusterUsesQualified2000QPSRuntimeProfile(t *testing.T) {
	for node := 1; node <= 3; node++ {
		path := filepath.Join("..", "..", "docker", "conf", fmt.Sprintf("node%d.toml", node))
		cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
		if err != nil {
			t.Fatalf("Load(%s) error = %v", path, err)
		}
		assertQualified2000QPSRuntimeProfile(t, cfg.StartupConfigSnapshot)
	}
}

func assertQualified2000QPSRuntimeProfile(t *testing.T, snapshot managementusecase.NodeConfigSnapshot) {
	t.Helper()
	wants := map[string]string{
		"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS":      "128",
		"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS":       "8",
		"WK_CLUSTER_CHANNEL_RPC_WORKERS":               "96",
		"WK_CLUSTER_CHANNEL_RPC_BATCH_MAX_ITEMS":       "8",
		"WK_GATEWAY_GNET_MULTICORE":                    "true",
		"WK_GATEWAY_TOKEN_AUTH_ON":                     "true",
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP":               "4",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS":        "1000",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY": "131072",
		"WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY":     "320",
	}
	for key, want := range wants {
		item, ok := snapshotItem(snapshot, key)
		if !ok {
			t.Errorf("startup snapshot missing %s", key)
			continue
		}
		if item.Value != want {
			t.Errorf("startup snapshot %s = %s, want %s", key, item.Value, want)
		}
	}
}
