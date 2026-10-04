package app

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/stretchr/testify/require"
)

func TestBeforeSendWebhookDefaultsAndIndependentWiring(t *testing.T) {
	cfg, err := NormalizeWebhookConfig(WebhookConfig{})
	require.NoError(t, err)
	require.False(t, cfg.BeforeSend.Enabled)
	require.Equal(t, 500*time.Millisecond, cfg.BeforeSend.Timeout)
	require.Equal(t, 256, cfg.BeforeSend.MaxInFlight)
	require.Equal(t, "deny", cfg.BeforeSend.OnTimeout)
	require.Equal(t, "deny", cfg.BeforeSend.OnError)

	pluginConfig := PluginConfig{}
	pluginConfig.SetEnableExplicit(true)
	app, err := newTestApp(t, Config{DataDir: t.TempDir(), Plugin: pluginConfig,
		Webhook: WebhookConfig{BeforeSend: BeforeSendWebhookConfig{Enabled: true, HTTPAddr: "https://business.example/hook"}},
	}, WithCluster(&fakeCluster{}), WithGateway(nil))
	require.NoError(t, err)
	require.Nil(t, app.plugins)
	require.Nil(t, app.webhook)
	require.NotNil(t, app.beforeSendWebhook)
	// Oversized input proves the configured admission runs before either HTTP
	// or the unwired authority submitter, even when plugin hooks are skipped.
	result, err := app.Messages().Send(context.Background(), message.SendCommand{
		FromUID: "u1", ChannelID: "g1", ChannelType: 2, SkipPluginHooks: true, Payload: make([]byte, codec.PayloadMaxSize+1),
	})
	require.NoError(t, err)
	require.Equal(t, message.ReasonInvalidRequest, result.Reason)
}
