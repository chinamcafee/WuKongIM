//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"
)

func TestWaitNodeReadySucceedsForStartedSingleNodeCluster(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = time.Millisecond
	cfg.Control.ClusterID = "readiness-single"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), realDiskClusterStartTimeout)
	defer startCancel()
	if err := node.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	readyCtx, readyCancel := context.WithTimeout(context.Background(), realDiskClusterReadyTimeout)
	defer readyCancel()
	if err := WaitNodeReady(readyCtx, node); err != nil {
		t.Fatalf("WaitNodeReady() error = %v", err)
	}
}
