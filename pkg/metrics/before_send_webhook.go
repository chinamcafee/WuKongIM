package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// BeforeSendWebhookMetrics records bounded synchronous admission outcomes.
type BeforeSendWebhookMetrics struct {
	total    *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newBeforeSendWebhookMetrics(registry prometheus.Registerer, labels prometheus.Labels) *BeforeSendWebhookMetrics {
	m := &BeforeSendWebhookMetrics{
		total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_webhook_before_send_total", Help: "Synchronous webhook admission outcomes.", ConstLabels: labels,
		}, []string{"result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wukongim_webhook_before_send_duration_seconds", Help: "Synchronous webhook admission latency in seconds.", ConstLabels: labels, Buckets: gatewayFrameDurationBuckets,
		}, []string{"result"}),
	}
	registry.MustRegister(m.total, m.duration)
	return m
}

// Observe preserves the closed decision vocabulary and prevents identity labels.
func (m *BeforeSendWebhookMetrics) Observe(result string, duration time.Duration) {
	if m == nil {
		return
	}
	switch result {
	case "allow", "reject", "error_allow", "error_deny", "timeout_allow", "timeout_deny", "overloaded", "invalid_request", "canceled":
	default:
		result = "other"
	}
	m.total.WithLabelValues(result).Inc()
	m.duration.WithLabelValues(result).Observe(duration.Seconds())
}
