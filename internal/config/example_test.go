package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
}

func TestCommandTOMLExampleLoads(t *testing.T) {
	cfg, err := Load(Options{Args: []string{"-config", filepath.Join("..", "..", "cmd", "wukongim", "wukongim.toml.example")}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(cmd example) error = %v", err)
	}
	if cfg.Cluster.Control.ClusterID != "wukongim-single" {
		t.Fatalf("ClusterID = %q, want wukongim-single", cfg.Cluster.Control.ClusterID)
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
	if cfg.API.ListenAddr != "0.0.0.0:5001" || cfg.API.ExternalTCPAddr != "192.168.10.109:5100" || cfg.API.ExternalWSAddr != "ws://192.168.10.109:5200" {
		t.Fatalf("API config = %#v", cfg.API)
	}
	if cfg.Manager.ListenAddr != "0.0.0.0:5300" || cfg.Manager.AuthOn {
		t.Fatalf("manager config = %#v", cfg.Manager)
	}
	if len(cfg.Gateway.Listeners) != 2 || !cfg.Message.PersonWhitelistEnabled || !cfg.Delivery.Enabled || !cfg.Plugin.Enable {
		t.Fatalf("runtime config = listeners:%d whitelist:%t delivery:%t plugin:%t", len(cfg.Gateway.Listeners), cfg.Message.PersonWhitelistEnabled, cfg.Delivery.Enabled, cfg.Plugin.Enable)
	}
	if !cfg.Webhook.Enabled || cfg.Webhook.HTTPAddr != "http://linku-im-processor:LinkU-WuKongIM-Webhook-2026@localhost/link-u-im-processor/webhook" ||
		!slices.Equal(cfg.Webhook.FocusEvents, []string{"msg.offline", "msg.notify"}) ||
		cfg.Webhook.NotifyBatchMaxItems != 100 || cfg.Webhook.NotifyBatchMaxWait != 500*time.Millisecond || cfg.Webhook.RetryMaxAttempts != 5 {
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

	want := "# Maximum recipient-authority delivery batches processed concurrently by this node.\n" +
		"# This is independent from channel_append.recipient_authority_dispatch_concurrency.\n" +
		"recipient_worker_concurrency = 100"
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
