//go:build integration

package client

import (
	"testing"
	"time"
)

func TestPacedTCPBenchmarkLatencyBudget(t *testing.T) {
	for _, tt := range []struct {
		name        string
		rate        int
		prefix      string
		slow        int
		latency     time.Duration
		wantFailure bool
	}{
		{"hosted tail", 500, "send", 141, 381 * time.Millisecond, false},
		{"hosted boundary", 500, "send", 141, 400 * time.Millisecond, false},
		{"hosted regression", 500, "send", 31, 401 * time.Millisecond, true},
		{"hosted one percent", 500, "send", 30, 401 * time.Millisecond, false},
		{"capacity keeps 200ms", 1000, "send", 141, 381 * time.Millisecond, true},
		{"stage remains diagnostic", 500, "gateway-handler", 141, 401 * time.Millisecond, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			latencies := make([]time.Duration, 3000)
			for i := range latencies {
				latencies[i] = 10 * time.Millisecond
				if i < tt.slow {
					latencies[i] = tt.latency
				}
			}
			reporter := &recordedPacedTCPBenchmark{metrics: make(map[string]float64)}
			reportPacedTCPLatency(reporter, tt.prefix, latencies, tt.rate)
			if reporter.failed != tt.wantFailure {
				t.Fatalf("failed = %v, want %v (metrics %v)", reporter.failed, tt.wantFailure, reporter.metrics)
			}
			if got := reporter.metrics[tt.prefix+"-over-200ms-pct"]; got != 100*float64(tt.slow)/3000 {
				t.Fatalf("200ms diagnostic = %v, want observed tail percentage", got)
			}
		})
	}
}

type recordedPacedTCPBenchmark struct {
	failed  bool
	metrics map[string]float64
}

func (*recordedPacedTCPBenchmark) Helper()                                   {}
func (*recordedPacedTCPBenchmark) Logf(string, ...any)                       {}
func (r *recordedPacedTCPBenchmark) ReportMetric(value float64, unit string) { r.metrics[unit] = value }
func (r *recordedPacedTCPBenchmark) Errorf(string, ...any)                   { r.failed = true }
