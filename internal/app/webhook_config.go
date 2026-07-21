package app

import (
	"context"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	runtimewebhook "github.com/WuKongIM/WuKongIM/internal/runtime/webhook"
)

const webhookConfigSnapshotSource = "startup_config"

// WebhookConfigSnapshot returns a read-only view of the normalized startup webhook configuration.
func (a *App) WebhookConfigSnapshot(context.Context) (accessmanager.WebhookConfigSnapshot, error) {
	cfg := a.cfg.Webhook
	return accessmanager.WebhookConfigSnapshot{
		Enabled:                   cfg.Enabled,
		HTTPAddr:                  cfg.HTTPAddr,
		FocusEvents:               cloneWebhookConfigStrings(cfg.FocusEvents),
		SupportedEvents:           supportedWebhookEvents(),
		QueueSize:                 cfg.QueueSize,
		Workers:                   cfg.Workers,
		OnlineStatusBatchMaxItems: cfg.OnlineBatchMaxItems,
		OnlineStatusBatchMaxWait:  cfg.OnlineBatchMaxWait.String(),
		OfflineUIDBatchSize:       cfg.OfflineUIDBatchSize,
		RequestTimeout:            cfg.RequestTimeout.String(),
		RetryMaxAttempts:          cfg.RetryMaxAttempts,
		OutboxDir:                 cfg.OutboxDir,
		OutboxMaxEntries:          cfg.OutboxMaxEntries,
		OutboxMaxBytes:            cfg.OutboxMaxBytes,
		OutboxDispatchBatchSize:   cfg.OutboxDispatchBatchSize,
		OutboxRetryBaseDelay:      cfg.OutboxRetryBaseDelay.String(),
		OutboxRetryMaxDelay:       cfg.OutboxRetryMaxDelay.String(),
		OutboxDeliveredRetention:  cfg.OutboxDeliveredRetention.String(),
		Source:                    webhookConfigSnapshotSource,
		RequiresRestart:           true,
	}, nil
}

// WebhookOutboxSnapshot returns node-local durable delivery health and a bounded dead-letter preview.
func (a *App) WebhookOutboxSnapshot(ctx context.Context) (accessmanager.WebhookOutboxSnapshot, error) {
	if a == nil || a.webhookOutbox == nil {
		return accessmanager.WebhookOutboxSnapshot{}, runtimewebhook.ErrOutboxClosed
	}
	stats, err := a.webhookOutbox.OutboxStats(ctx)
	if err != nil {
		return accessmanager.WebhookOutboxSnapshot{}, err
	}
	deadLetters := make([]accessmanager.WebhookDeadLetter, 0, len(stats.DeadLetterEntries))
	for _, entry := range stats.DeadLetterEntries {
		deadLetters = append(deadLetters, accessmanager.WebhookDeadLetter{
			ID: entry.ID, Event: entry.Event, Items: entry.Items, Attempt: entry.Attempt,
			CreatedAt: entry.CreatedAt, LastErrorCode: entry.LastErrorCode,
		})
	}
	snapshot := accessmanager.WebhookOutboxSnapshot{
		Backlog: stats.Backlog, DeadLetterCount: stats.DeadLetters,
		DeliveredTombstones: stats.DeliveredTombstones, LogicalBytes: stats.LogicalBytes,
		OldestAge: stats.OldestAge.String(), RetryAttempts: stats.RetryAttempts,
		DeadLetters: deadLetters,
	}
	if !stats.OldestCreatedAt.IsZero() {
		oldest := stats.OldestCreatedAt
		snapshot.OldestCreatedAt = &oldest
	}
	return snapshot, nil
}

// ReplayWebhookDeadLetters requeues only the operator-selected stable delivery identities.
func (a *App) ReplayWebhookDeadLetters(ctx context.Context, ids []string) (accessmanager.WebhookOutboxReplayResponse, error) {
	if a == nil || a.webhookOutbox == nil {
		return accessmanager.WebhookOutboxReplayResponse{}, runtimewebhook.ErrOutboxClosed
	}
	replayed, err := a.webhookOutbox.ReplayDeadLetters(ctx, ids)
	if err != nil {
		return accessmanager.WebhookOutboxReplayResponse{}, err
	}
	return accessmanager.WebhookOutboxReplayResponse{Requested: len(ids), Replayed: replayed}, nil
}

func supportedWebhookEvents() []string {
	return []string{
		runtimewebhook.EventMsgNotify,
		runtimewebhook.EventMsgOffline,
		runtimewebhook.EventUserOnlineStatus,
	}
}

func cloneWebhookConfigStrings(values []string) []string {
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
