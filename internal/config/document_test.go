package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/pelletier/go-toml/v2"
)

func documentForTest(t *testing.T, values map[string]string) management.NodeConfigDocument {
	t.Helper()
	cfg, err := buildConfig(values)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for key := range values {
		sources[key] = management.NodeConfigValueSourceEnvironment
	}
	document, err := buildStartupDocument(sourceValues{values: values, sources: sources}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestStartupDocumentCoversSchemaWithTypedDefaultsAndBilingualHelp(t *testing.T) {
	document := documentForTest(t, minimalBuildValues())
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(document.TOML), &parsed); err != nil {
		t.Fatal(err)
	}
	flat := map[string]any{}
	flattenTOML("", parsed, flat)
	if len(document.Fields) != len(schemaFields) {
		t.Fatalf("fields=%d, schema=%d", len(document.Fields), len(schemaFields))
	}
	seen := map[string]bool{}
	lines := strings.Split(document.TOML, "\n")
	for _, field := range document.Fields {
		if seen[field.Path] {
			t.Fatalf("duplicate field %s", field.Path)
		}
		seen[field.Path] = true
		if field.Description == "" || field.DescriptionZH == "" {
			t.Fatalf("missing help: %s", field.Path)
		}
		_, present := flat[field.Path]
		if present == field.Redacted {
			t.Fatalf("redaction/type contract for %s", field.Path)
		}
		if field.Redacted && !strings.HasPrefix(lines[field.Line-1], "# ") {
			t.Fatalf("missing redaction comment for %s", field.Path)
		}
	}
	for path, want := range map[string]any{
		"cluster.hash_slot_count": int64(256), "cluster.initial_slot_count": int64(1),
		"cluster.slot_tick_interval": "50ms", "cluster.slot_election_tick": int64(40),
		"cluster.channel_store_append_workers": int64(128), "cluster.channel_store_apply_workers": int64(8),
		"cluster.commit_coordinator_sync": true, "cluster.commit_coordinator_flush_window": "500µs",
		"cluster.channel_append_batch_max_wait": "1ms", "gateway.send_timeout": "5s",
		"gateway.token_auth_on": true, "delivery.recipient_worker_concurrency": int64(320),
		"diagnostics.slow_threshold_ms": int64(500),
	} {
		if !reflect.DeepEqual(flat[path], want) {
			t.Errorf("%s=%#v, want %#v", path, flat[path], want)
		}
	}
	for _, field := range schemaFields {
		if field.Sensitive || isPathLikeField(field.TOMLPath) || field.Kind == kindObjectList {
			continue
		}
		value := flat[field.TOMLPath]
		switch field.Kind {
		case kindBool:
			if _, ok := value.(bool); !ok {
				t.Errorf("%s must be a bool", field.TOMLPath)
			}
		case kindInt, kindUint16, kindUint32, kindUint64:
			if _, ok := value.(int64); !ok {
				t.Errorf("%s must be an integer: %T", field.TOMLPath, value)
			}
		case kindFloat:
			if _, ok := value.(float64); !ok {
				t.Errorf("%s must be a float: %T", field.TOMLPath, value)
			}
		case kindString, kindDuration:
			if _, ok := value.(string); !ok {
				t.Errorf("%s must be a string", field.TOMLPath)
			}
		case kindStringList:
			if _, ok := value.([]any); !ok {
				t.Errorf("%s must be an array: %T", field.TOMLPath, value)
			}
		}
	}
}

func TestStartupDocumentOverridesEscapingProvenanceAndRedaction(t *testing.T) {
	values := minimalBuildValues()
	values["WK_MANAGER_JWT_SECRET"] = "SECRET_CANARY"
	values["WK_MANAGER_USERS"] = `[{"username":"admin","password":"PASSWORD_CANARY"}]`
	values["WK_CLUSTER_JOIN_TOKEN"] = "JOIN_CANARY"
	values["WK_PLUGIN_DIR"] = "/private/PATH_CANARY"
	values["WK_GATEWAY_LISTENERS"] = `[{"name":"OBJECT_CANARY","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`
	values["WK_MANAGER_JWT_ISSUER"] = "quoted \"issuer\"\nnext line"
	values["WK_GATEWAY_TOKEN_AUTH_ON"] = "false"
	values["WK_GATEWAY_RUNTIME_ASYNC_AUTH_WORKERS"] = "23"
	values["WK_CLUSTER_SLOT_TICK_INTERVAL"] = "100ms"
	values["WK_CLUSTER_HASH_SLOT_COUNT"] = "0"
	values["WK_DIAGNOSTICS_SLOW_THRESHOLD_MS"] = "712"
	values["WK_DIAGNOSTICS_SAMPLE_RATE"] = "0.25"
	values["WK_WEBHOOK_FOCUS_EVENTS"] = `["msg.notify","user.online"]`
	document := documentForTest(t, values)
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"SECRET_CANARY", "PASSWORD_CANARY", "JOIN_CANARY", "PATH_CANARY", "OBJECT_CANARY"} {
		if strings.Contains(string(body), canary) {
			t.Errorf("leaked %s", canary)
		}
	}
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(document.TOML), &parsed); err != nil {
		t.Fatal(err)
	}
	flat := map[string]any{}
	flattenTOML("", parsed, flat)
	for path, want := range map[string]any{
		"manager.jwt_issuer":    values["WK_MANAGER_JWT_ISSUER"],
		"gateway.token_auth_on": false, "gateway.runtime_async_auth_workers": int64(23),
		"cluster.slot_tick_interval": "100ms", "cluster.hash_slot_count": int64(256),
		"diagnostics.slow_threshold_ms": int64(712), "diagnostics.sample_rate": 0.25,
		"webhook.focus_events": []any{"msg.notify", "user.online"},
	} {
		if !reflect.DeepEqual(flat[path], want) {
			t.Errorf("%s=%#v, want %#v", path, flat[path], want)
		}
	}
	for _, field := range document.Fields {
		if field.Path == "cluster.hash_slot_count" && field.Source != "derived" {
			t.Errorf("zero normalization source=%s", field.Source)
		}
		if field.Path == "cluster.slot_tick_interval" && field.Source != "env" {
			t.Errorf("explicit source=%s", field.Source)
		}
	}
}

func TestStartupDocumentRetainsTOMLSourceAndEnvironmentPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	if err := os.WriteFile(path, []byte(`[node]
id = 1
data_dir = "/private/node-config-document-test"
[cluster]
listen_addr = "127.0.0.1:7001"
slot_tick_interval = "1000ms"
[gateway]
token_auth_on = true
`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: []string{"WK_GATEWAY_TOKEN_AUTH_ON=false"}})
	if err != nil {
		t.Fatal(err)
	}
	document := cfg.StartupConfigDocument
	if err := document.Validate(1); err != nil || document.GeneratedAt.IsZero() {
		t.Fatalf("loader did not attach a startup document: %v", err)
	}
	if !strings.Contains(document.TOML, "token_auth_on = false") {
		t.Fatal("environment override missing from startup document")
	}
	for _, field := range document.Fields {
		switch field.Path {
		case "cluster.slot_tick_interval":
			if field.Source != "toml" {
				t.Errorf("equivalent duration lost provenance: %s", field.Source)
			}
		case "gateway.token_auth_on":
			if field.Source != "env" {
				t.Errorf("override source=%s", field.Source)
			}
		}
	}
}
