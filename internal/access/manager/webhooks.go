package manager

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// WebhookConfigProvider returns the current process webhook startup configuration.
type WebhookConfigProvider interface {
	// WebhookConfigSnapshot returns a read-only snapshot of the effective webhook configuration.
	WebhookConfigSnapshot(context.Context) (WebhookConfigSnapshot, error)
}

// WebhookOutboxProvider exposes node-local durable webhook operations.
type WebhookOutboxProvider interface {
	WebhookOutboxSnapshot(context.Context) (WebhookOutboxSnapshot, error)
	ReplayWebhookDeadLetters(context.Context, []string) (WebhookOutboxReplayResponse, error)
}

// WebhookOutboxSnapshot is a body-free operational view of the durable outbox.
type WebhookOutboxSnapshot struct {
	Backlog             int                 `json:"backlog"`
	DeadLetterCount     int                 `json:"dead_letter_count"`
	DeliveredTombstones int                 `json:"delivered_tombstones"`
	LogicalBytes        int64               `json:"logical_bytes"`
	OldestAge           string              `json:"oldest_age"`
	RetryAttempts       int64               `json:"retry_attempts"`
	OldestCreatedAt     *time.Time          `json:"oldest_created_at,omitempty"`
	DeadLetters         []WebhookDeadLetter `json:"dead_letters"`
}

// WebhookDeadLetter identifies one replayable delivery without exposing its payload.
type WebhookDeadLetter struct {
	ID            string    `json:"id"`
	Event         string    `json:"event"`
	Items         int       `json:"items"`
	Attempt       int       `json:"attempt"`
	CreatedAt     time.Time `json:"created_at"`
	LastErrorCode string    `json:"last_error_code"`
}

type webhookOutboxReplayRequest struct {
	IDs []string `json:"ids"`
}

// WebhookOutboxReplayResponse reports an explicit bounded replay operation.
type WebhookOutboxReplayResponse struct {
	Requested int `json:"requested"`
	Replayed  int `json:"replayed"`
}

// WebhookConfigSnapshot is the manager JSON body for read-only webhook configuration.
type WebhookConfigSnapshot struct {
	// Enabled reports whether the node-local webhook runtime is enabled.
	Enabled bool `json:"enabled"`
	// HTTPAddr is the configured webhook callback endpoint.
	HTTPAddr string `json:"http_addr"`
	// FocusEvents lists the configured event filter; an empty list means all supported events.
	FocusEvents []string `json:"focus_events"`
	// SupportedEvents lists every webhook event supported by this process.
	SupportedEvents []string `json:"supported_events"`
	// QueueSize bounds best-effort online-status events waiting in memory.
	QueueSize int `json:"queue_size"`
	// Workers bounds concurrent webhook sender calls per event queue.
	Workers int `json:"workers"`
	// OnlineStatusBatchMaxItems limits user.onlinestatus records sent in one webhook request.
	OnlineStatusBatchMaxItems int `json:"online_status_batch_max_items"`
	// OnlineStatusBatchMaxWait is the formatted max wait before a partial user.onlinestatus batch is sent.
	OnlineStatusBatchMaxWait string `json:"online_status_batch_max_wait"`
	// OfflineUIDBatchSize limits offline recipient UIDs sent in one msg.offline request.
	OfflineUIDBatchSize int `json:"offline_uid_batch_size"`
	// RequestTimeout is the formatted timeout for one outbound webhook request attempt.
	RequestTimeout string `json:"request_timeout"`
	// RetryMaxAttempts moves critical delivery to dead-letter and ends best-effort delivery after this many attempts.
	RetryMaxAttempts int `json:"retry_max_attempts"`
	// OutboxDir stores critical webhook delivery state on this node.
	OutboxDir string `json:"outbox_dir"`
	// OutboxMaxEntries bounds pending and dead delivery records.
	OutboxMaxEntries int `json:"outbox_max_entries"`
	// OutboxMaxBytes bounds logical outbox storage usage.
	OutboxMaxBytes int64 `json:"outbox_max_bytes"`
	// OutboxDispatchBatchSize bounds records claimed by one dispatcher pass.
	OutboxDispatchBatchSize int `json:"outbox_dispatch_batch_size"`
	// OutboxRetryBaseDelay is the formatted first durable retry delay.
	OutboxRetryBaseDelay string `json:"outbox_retry_base_delay"`
	// OutboxRetryMaxDelay is the formatted exponential retry cap.
	OutboxRetryMaxDelay string `json:"outbox_retry_max_delay"`
	// OutboxDeliveredRetention is the formatted delivered-id dedupe retention.
	OutboxDeliveredRetention string `json:"outbox_delivered_retention"`
	// Source identifies where this snapshot was derived from.
	Source string `json:"source"`
	// RequiresRestart reports whether changing this configuration requires a process restart.
	RequiresRestart bool `json:"requires_restart"`
}

func (s *Server) handleWebhookConfig(c *gin.Context) {
	if s == nil || s.webhookConfig == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "webhook config provider is not configured")
		return
	}
	snapshot, err := s.webhookConfig.WebhookConfigSnapshot(c.Request.Context())
	if err != nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "webhook config unavailable")
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func (s *Server) handleWebhookOutbox(c *gin.Context) {
	if s == nil || s.webhookOutbox == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "webhook outbox provider is not configured")
		return
	}
	snapshot, err := s.webhookOutbox.WebhookOutboxSnapshot(c.Request.Context())
	if err != nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "webhook outbox unavailable")
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func (s *Server) handleWebhookOutboxReplay(c *gin.Context) {
	if s == nil || s.webhookOutbox == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "webhook outbox provider is not configured")
		return
	}
	var request webhookOutboxReplayRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(request.IDs) == 0 || len(request.IDs) > 100 {
		jsonError(c, http.StatusBadRequest, "bad_request", "replay requires 1..100 webhook ids")
		return
	}
	seen := make(map[string]struct{}, len(request.IDs))
	for _, id := range request.IDs {
		if id == "" || strings.TrimSpace(id) != id {
			jsonError(c, http.StatusBadRequest, "bad_request", "invalid webhook id")
			return
		}
		if _, exists := seen[id]; exists {
			jsonError(c, http.StatusBadRequest, "bad_request", "duplicate webhook id")
			return
		}
		seen[id] = struct{}{}
	}
	response, err := s.webhookOutbox.ReplayWebhookDeadLetters(c.Request.Context(), request.IDs)
	if err != nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "webhook dead-letter replay failed")
		return
	}
	c.JSON(http.StatusOK, response)
}
