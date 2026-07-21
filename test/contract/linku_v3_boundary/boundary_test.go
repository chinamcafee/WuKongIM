package linku_v3_boundary_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProductRuntimeHasOnlyV3HealthAndReadinessSurfaces(t *testing.T) {
	root := repositoryRoot(t)
	for _, relative := range []string{"docker-compose.yaml", "docker/cluster/docker-compose.yaml"} {
		content := read(t, filepath.Join(root, relative))
		if !strings.Contains(content, "/readyz") || strings.Contains(content, "/health ") {
			t.Fatalf("%s must use /readyz and must not use legacy /health", relative)
		}
	}
	api := read(t, filepath.Join(root, "internal/access/api/server.go"))
	for _, route := range []string{"/healthz", "/readyz"} {
		if !strings.Contains(api, route) {
			t.Fatalf("product API is missing %s", route)
		}
	}
	for _, legacy := range []string{"\"/health\"", "\"/varz\"", "\"/connz\""} {
		if strings.Contains(api, legacy) {
			t.Fatalf("product API still exposes legacy route %s", legacy)
		}
	}
}

func TestLegacyVueManagerClientIsRemoved(t *testing.T) {
	root := repositoryRoot(t)
	legacyPaths := []string{
		"web/src/main.ts",
		"web/src/router",
		"web/src/services",
	}
	for _, relative := range legacyPaths {
		if _, err := os.Stat(filepath.Join(root, relative)); !os.IsNotExist(err) {
			t.Fatalf("legacy manager artifact still exists: %s", relative)
		}
	}
	err := filepath.WalkDir(filepath.Join(root, "web/src"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".vue") {
			t.Errorf("legacy Vue manager file still exists: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSuppliedLinkUConfigurationsRequireTokenAuthAndSeparateManager(t *testing.T) {
	root := repositoryRoot(t)
	files := []string{
		"config/wukongim-v3-single-node.toml",
		"docker/conf/node1.toml",
		"docker/conf/node2.toml",
		"docker/conf/node3.toml",
		"scripts/wukongim/wukongim.toml",
		"scripts/wukongim/wukongim-node1.toml",
		"scripts/wukongim/wukongim-node2.toml",
		"scripts/wukongim/wukongim-node3.toml",
	}
	for _, relative := range files {
		content := read(t, filepath.Join(root, relative))
		if !strings.Contains(content, "token_auth_enabled = true") {
			t.Fatalf("%s does not explicitly require gateway token auth", relative)
		}
	}
	single := read(t, filepath.Join(root, "config/wukongim-v3-single-node.toml"))
	for _, required := range []string{
		"listen_addr = \"127.0.0.1:5001\"",
		"listen_addr = \"127.0.0.1:5300\"",
		"auth_on = true",
		"metrics_enable = true",
	} {
		if !strings.Contains(single, required) {
			t.Fatalf("single-node Link-U config is missing %q", required)
		}
	}
}

func TestProductionGatewayIsPinnedToWKProtoV6(t *testing.T) {
	root := repositoryRoot(t)
	wiring := read(t, filepath.Join(root, "internal/app/wiring.go"))
	if !strings.Contains(wiring, "RequiredProtocolVersion: frame.LatestVersion") {
		t.Fatal("production gateway is not pinned to WKProto v6")
	}
	auth := read(t, filepath.Join(root, "pkg/gateway/auth.go"))
	if !strings.Contains(auth, "ReasonProtocolUpgradeRequired") {
		t.Fatal("gateway does not fail closed for obsolete protocol versions")
	}
	adapter := read(t, filepath.Join(root, "pkg/gateway/protocol/wkproto/adapter.go"))
	if strings.Contains(adapter, "version = uint8(frame.LegacyMessageSeqVersion)") {
		t.Fatal("production adapter still defaults outbound frames to WKProto v5")
	}

	for _, relative := range []string{
		"internal/access/api/server.go",
		"internal/access/api/message_send.go",
		"internal/access/api/channel_messagesync.go",
	} {
		content := read(t, filepath.Join(root, relative))
		if strings.Contains(content, "/channel/max_message_seq") {
			t.Fatalf("%s restored removed v2 max-message-seq API", relative)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
