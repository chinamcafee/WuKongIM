package app

import (
	"fmt"
	"time"

	webhookinfra "github.com/WuKongIM/WuKongIM/internal/infra/webhook"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
)

// BeforeSendWebhookConfig controls synchronous business admission independently
// of asynchronous webhook endpoints, focus_events, and plugin enablement.
type BeforeSendWebhookConfig struct {
	// Enabled opts every message entry into the callback; omission disables it.
	Enabled bool
	// HTTPAddr is the HTTP(S) endpoint called with event=msg.before_send.
	// Userinfo and fragments are forbidden; diagnostic projections redact the URL.
	HTTPAddr string
	// Timeout bounds one callback; zero uses 500ms. The send deadline still wins.
	Timeout time.Duration
	// OnTimeout selects allow or deny for callback timeout, defaulting to deny.
	OnTimeout string
	// OnError selects allow or deny for transport, status, or response errors.
	// It defaults to deny and never overrides explicit business rejection.
	OnError string
	// MaxInFlight bounds running callbacks on this node; zero uses 256.
	// Saturation rejects immediately without queueing or failure-open fallback.
	MaxInFlight int
}

func defaultBeforeSendWebhookConfig(cfg BeforeSendWebhookConfig) BeforeSendWebhookConfig {
	if cfg.Timeout == 0 {
		cfg.Timeout = 500 * time.Millisecond
	}
	if cfg.OnTimeout == "" {
		cfg.OnTimeout = "deny"
	}
	if cfg.OnError == "" {
		cfg.OnError = "deny"
	}
	if cfg.MaxInFlight == 0 {
		cfg.MaxInFlight = 256
	}
	return cfg
}

func validateBeforeSendWebhookConfig(cfg BeforeSendWebhookConfig) error {
	if cfg.Timeout <= 0 || cfg.MaxInFlight <= 0 {
		return fmt.Errorf("%w: webhook.before_send limits must be positive", ErrInvalidConfig)
	}
	if (cfg.OnTimeout != "allow" && cfg.OnTimeout != "deny") || (cfg.OnError != "allow" && cfg.OnError != "deny") {
		return fmt.Errorf("%w: webhook.before_send policies must be allow or deny", ErrInvalidConfig)
	}
	if cfg.Enabled || cfg.HTTPAddr != "" {
		if _, err := webhookinfra.NewBeforeSendClient(cfg.HTTPAddr, nil); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidConfig, err)
		}
	}
	return nil
}

func (a *App) wireBeforeSendWebhook() error {
	cfg := a.cfg.Webhook.BeforeSend
	if !cfg.Enabled || a.beforeSendWebhook != nil {
		return nil
	}
	client, err := webhookinfra.NewBeforeSendClient(cfg.HTTPAddr, nil)
	if err != nil {
		return err
	}
	a.beforeSendWebhook, err = message.NewBeforeSendWebhook(message.BeforeSendOptions{
		Caller: client, Timeout: cfg.Timeout, OnTimeout: cfg.OnTimeout,
		OnError: cfg.OnError, MaxInFlight: cfg.MaxInFlight,
		MaxPayloadBytes: codec.PayloadMaxSize, Observe: a.observeBeforeSendWebhook,
	})
	return err
}

func (a *App) observeBeforeSendWebhook(result string, duration time.Duration) {
	if a.metrics == nil {
		return
	}
	a.metrics.BeforeSendWebhook.Observe(result, duration)
}
