package metrics

import (
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestBeforeSendWebhookMetricsPreserveBoundedOutcomes(t *testing.T) {
	r := New(1, "node1")
	for _, result := range []string{"allow", "reject", "error_allow", "error_deny", "timeout_allow", "timeout_deny", "overloaded", "invalid_request", "canceled"} {
		r.BeforeSendWebhook.Observe(result, time.Millisecond)
		require.Equal(t, float64(1), testutil.ToFloat64(r.BeforeSendWebhook.total.WithLabelValues(result)))
	}
	r.BeforeSendWebhook.Observe("uid-must-not-be-a-label", time.Millisecond)
	require.Equal(t, float64(1), testutil.ToFloat64(r.BeforeSendWebhook.total.WithLabelValues("other")))
}
