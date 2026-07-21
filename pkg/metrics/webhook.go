package metrics

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// WebhookMetrics exposes durable webhook delivery and outbox health.
type WebhookMetrics struct {
	deliveryTotal   *prometheus.CounterVec
	deliveryLatency *prometheus.HistogramVec
	retryTotal      *prometheus.CounterVec
	backlog         prometheus.Gauge
	oldestAge       prometheus.Gauge
	deadLetters     prometheus.Gauge
	logicalBytes    prometheus.Gauge
	liveRetries     prometheus.Gauge
}

func newWebhookMetrics(registry prometheus.Registerer, labels prometheus.Labels) *WebhookMetrics {
	m := &WebhookMetrics{
		deliveryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_webhook_delivery_total", Help: "Webhook operations by event and bounded result.", ConstLabels: labels,
		}, []string{"event", "result"}),
		deliveryLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_webhook_delivery_latency_seconds", Help: "Successful and failed webhook HTTP attempt latency.", ConstLabels: labels,
			Buckets: gatewayFrameDurationBuckets,
		}, []string{"event", "result"}),
		retryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_webhook_outbox_retry_total", Help: "Durable webhook retry/dead-letter transitions.", ConstLabels: labels,
		}, []string{"event", "result"}),
		backlog: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_webhook_outbox_backlog", Help: "Current pending durable webhook records.", ConstLabels: labels,
		}),
		oldestAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_webhook_outbox_oldest_age_seconds", Help: "Age of the oldest pending or dead webhook record.", ConstLabels: labels,
		}),
		deadLetters: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_webhook_outbox_dead_letters", Help: "Current durable webhook dead-letter records.", ConstLabels: labels,
		}),
		logicalBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_webhook_outbox_logical_bytes", Help: "Logical key/value bytes retained by the webhook outbox.", ConstLabels: labels,
		}),
		liveRetries: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wukongim_webhook_outbox_live_retry_attempts", Help: "Retry attempts represented by current pending and dead records.", ConstLabels: labels,
		}),
	}
	registry.MustRegister(m.deliveryTotal, m.deliveryLatency, m.retryTotal, m.backlog,
		m.oldestAge, m.deadLetters, m.logicalBytes, m.liveRetries)
	return m
}

// Observe records one bounded runtime transition.
func (m *WebhookMetrics) Observe(event, result string, items int, duration time.Duration) {
	if m == nil {
		return
	}
	event = normalizeWebhookMetricLabel(event, "unknown")
	result = normalizeWebhookMetricLabel(result, "unknown")
	weight := float64(items)
	if weight <= 0 {
		weight = 1
	}
	m.deliveryTotal.WithLabelValues(event, result).Add(weight)
	if duration > 0 {
		m.deliveryLatency.WithLabelValues(event, result).Observe(duration.Seconds())
	}
	if result == "retry" || result == "dead_letter" {
		m.retryTotal.WithLabelValues(event, result).Inc()
	}
}

// SetOutbox updates gauges from one consistent durable snapshot.
func (m *WebhookMetrics) SetOutbox(backlog, dead int, oldestAge time.Duration, logicalBytes, liveRetries int64) {
	if m == nil {
		return
	}
	m.backlog.Set(float64(max(backlog, 0)))
	m.deadLetters.Set(float64(max(dead, 0)))
	m.oldestAge.Set(max(oldestAge.Seconds(), 0))
	m.logicalBytes.Set(float64(max(logicalBytes, 0)))
	m.liveRetries.Set(float64(max(liveRetries, 0)))
}

func normalizeWebhookMetricLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
