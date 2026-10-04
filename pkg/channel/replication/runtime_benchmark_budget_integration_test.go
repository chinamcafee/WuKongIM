//go:build integration

package replication_test

import (
	"testing"
	"time"
)

// The hosted workflow promises a 400ms p99 gate. Replay its latency tail
// through the actual benchmark reporter without depending on runner disk speed.
func TestChannelAppendBenchmarkLatencyBudget(t *testing.T) {
	for _, tt := range []struct {
		name        string
		rate        int
		slow        int
		latency     time.Duration
		wantFailure bool
	}{
		{"hosted CI tail", 500, 141, 381 * time.Millisecond, false},
		{"hosted boundary", 500, 141, 400 * time.Millisecond, false},
		{"hosted regression", 500, 31, 401 * time.Millisecond, true},
		{"hosted one percent", 500, 30, 401 * time.Millisecond, false},
		{"capacity keeps 200ms", 1000, 141, 381 * time.Millisecond, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			latencies := make([]time.Duration, 3000)
			for i := range latencies {
				latencies[i] = 10 * time.Millisecond
				if i < tt.slow {
					latencies[i] = tt.latency
				}
			}
			reporter := &recordedQuorumBenchmark{metrics: make(map[string]float64)}
			reportDurableQuorumBenchmark(reporter, "append", latencies, durableQuorumCommitSnapshot{}, tt.rate)
			if reporter.failed != tt.wantFailure {
				t.Fatalf("failed = %v, want %v (metrics %v)", reporter.failed, tt.wantFailure, reporter.metrics)
			}
			if got := reporter.metrics["append-over-200ms-pct"]; got != 100*float64(tt.slow)/3000 {
				t.Fatalf("200ms diagnostic = %v, want observed tail percentage", got)
			}
		})
	}
}

type recordedQuorumBenchmark struct {
	failed  bool
	metrics map[string]float64
}

func (*recordedQuorumBenchmark) Helper()                                   {}
func (r *recordedQuorumBenchmark) ReportMetric(value float64, unit string) { r.metrics[unit] = value }
func (r *recordedQuorumBenchmark) Errorf(string, ...any)                   { r.failed = true }
