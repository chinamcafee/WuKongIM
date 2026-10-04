package config

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadBeforeSendWebhookTOMLAndEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "./data"
[cluster]
listen_addr = "127.0.0.1:7001"
[webhook.before_send]
enabled = true
http_addr = "https://business.example/hook"
timeout = "250ms"
on_timeout = "allow"
on_error = "deny"
max_in_flight = 17
[plugin]
enable = false
`)
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	require.NoError(t, err)
	require.True(t, cfg.Webhook.BeforeSend.Enabled)
	require.False(t, cfg.Webhook.Enabled)
	require.False(t, cfg.Plugin.Enable)
	require.Equal(t, 250*time.Millisecond, cfg.Webhook.BeforeSend.Timeout)
	require.Equal(t, 17, cfg.Webhook.BeforeSend.MaxInFlight)
	require.Equal(t, "allow", cfg.Webhook.BeforeSend.OnTimeout)
	cfg, err = Load(Options{Args: []string{"-config", path}, Environ: append(cleanEnv(),
		"WK_WEBHOOK_BEFORE_SEND_ENABLED=false", "WK_WEBHOOK_BEFORE_SEND_HTTP_ADDR=https://override.example/hook",
		"WK_WEBHOOK_BEFORE_SEND_TIMEOUT=100ms", "WK_WEBHOOK_BEFORE_SEND_ON_TIMEOUT=deny",
		"WK_WEBHOOK_BEFORE_SEND_ON_ERROR=allow", "WK_WEBHOOK_BEFORE_SEND_MAX_IN_FLIGHT=8")})
	require.NoError(t, err)
	require.False(t, cfg.Webhook.BeforeSend.Enabled)
	require.Equal(t, "https://override.example/hook", cfg.Webhook.BeforeSend.HTTPAddr)
	require.Equal(t, 100*time.Millisecond, cfg.Webhook.BeforeSend.Timeout)
	require.Equal(t, 8, cfg.Webhook.BeforeSend.MaxInFlight)
	require.Equal(t, "deny", cfg.Webhook.BeforeSend.OnTimeout)
	require.Equal(t, "allow", cfg.Webhook.BeforeSend.OnError)
}

func TestLoadBeforeSendWebhookRejectsInvalidConfig(t *testing.T) {
	for _, setting := range []string{
		"enabled = true", "timeout = \"-1ms\"", "max_in_flight = -1",
		"on_timeout = \"ignore\"", "on_error = \"ignore\"",
		"http_addr = \"file:///tmp/hook\"", "http_addr = \"https://user:secret@example.com/hook\"",
	} {
		path := filepath.Join(t.TempDir(), "wukongim.toml")
		writeFile(t, path, "[node]\nid = 1\ndata_dir = \"./data\"\n[cluster]\nlisten_addr = \"127.0.0.1:7001\"\n[webhook.before_send]\n"+setting+"\n")
		_, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
		require.Error(t, err, setting)
		require.NotContains(t, err.Error(), "user:secret")
	}
}

func TestBeforeSendWebhookURLIsRedacted(t *testing.T) {
	redacted, err := RedactDiagnosticTOML([]byte("[webhook.before_send]\nhttp_addr = \"https://sensitive.example/hook\"\n"))
	require.NoError(t, err)
	require.NotContains(t, string(redacted), "sensitive.example")
}
