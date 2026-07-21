package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWebhookMetricsExposeDeliveryRetryAndOutboxHealth(t *testing.T) {
	registry := New(3, "node-3")
	require.NotNil(t, registry.Webhook)

	registry.Webhook.Observe("msg.notify", "ok", 2, 25*time.Millisecond)
	registry.Webhook.Observe("msg.notify", "retry", 1, 40*time.Millisecond)
	registry.Webhook.Observe("msg.offline", "dead_letter", 3, 50*time.Millisecond)
	registry.Webhook.SetOutbox(7, 2, 90*time.Second, 4096, 5)

	families, err := registry.Gather()
	require.NoError(t, err)

	delivery := requireMetricFamily(t, families, "wukongim_webhook_delivery_total")
	require.Equal(t, float64(2), findMetricByLabels(t, delivery, map[string]string{
		"node_id": "3", "node_name": "node-3", "event": "msg.notify", "result": "ok",
	}).GetCounter().GetValue())
	require.Equal(t, float64(3), findMetricByLabels(t, delivery, map[string]string{
		"node_id": "3", "node_name": "node-3", "event": "msg.offline", "result": "dead_letter",
	}).GetCounter().GetValue())

	retries := requireMetricFamily(t, families, "wukongim_webhook_outbox_retry_total")
	require.Equal(t, float64(1), findMetricByLabels(t, retries, map[string]string{
		"node_id": "3", "node_name": "node-3", "event": "msg.notify", "result": "retry",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, retries, map[string]string{
		"node_id": "3", "node_name": "node-3", "event": "msg.offline", "result": "dead_letter",
	}).GetCounter().GetValue())

	for name, want := range map[string]float64{
		"wukongim_webhook_outbox_backlog":             7,
		"wukongim_webhook_outbox_dead_letters":        2,
		"wukongim_webhook_outbox_oldest_age_seconds":  90,
		"wukongim_webhook_outbox_logical_bytes":       4096,
		"wukongim_webhook_outbox_live_retry_attempts": 5,
	} {
		family := requireMetricFamily(t, families, name)
		require.Len(t, family.GetMetric(), 1)
		require.Equal(t, want, family.GetMetric()[0].GetGauge().GetValue(), name)
	}
}
