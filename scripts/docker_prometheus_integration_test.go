//go:build integration

package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestDockerImageEmbeddedPrometheus validates an exact, already-built image so
// the publication gate and local regression loop exercise the same artifact.
func TestDockerImageEmbeddedPrometheus(t *testing.T) {
	image := os.Getenv("WK_DOCKER_PROMETHEUS_IMAGE")
	if image == "" {
		t.Skip("set WK_DOCKER_PROMETHEUS_IMAGE to the exact image to validate")
	}
	platform := os.Getenv("WK_DOCKER_PROMETHEUS_PLATFORM")
	if platform == "" {
		t.Fatal("WK_DOCKER_PROMETHEUS_PLATFORM must select linux/amd64 or linux/arm64")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "scripts/verify-docker-prometheus.sh", image, platform)
	cmd.Dir = repoRoot(t)
	// Give the verifier's trap time to collect failure logs and release its exact
	// container and volume if the outer deadline is reached.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 40 * time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("embedded Prometheus image contract: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
