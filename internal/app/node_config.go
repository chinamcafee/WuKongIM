package app

import (
	"context"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

// NodeConfigSnapshot returns this process's allowlisted effective startup configuration.
func (a *App) NodeConfigSnapshot(_ context.Context, requestedNodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	if a == nil {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	cfg := a.cfg
	nodeID := cfg.NodeID
	if nodeID == 0 && cfg.Cluster.NodeID != 0 {
		nodeID = cfg.Cluster.NodeID
	}
	if nodeID == 0 && cfg.StartupConfigSnapshot.NodeID != 0 {
		nodeID = cfg.StartupConfigSnapshot.NodeID
	}
	if requestedNodeID != nodeID {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	if len(cfg.StartupConfigSnapshot.Groups) == 0 {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	snapshot := cfg.StartupConfigSnapshot
	snapshot.GeneratedAt = time.Now().UTC()
	snapshot.NodeID = nodeID
	if snapshot.Source == "" {
		snapshot.Source = managementusecase.NodeConfigSnapshotSourceEffectiveStartup
	}
	snapshot.RequiresRestart = true
	return snapshot, nil
}

// NodeConfigDocument returns an owned copy of this node's redacted startup document.
func (a *App) NodeConfigDocument(ctx context.Context, requestedNodeID uint64) (managementusecase.NodeConfigDocument, error) {
	if err := ctx.Err(); err != nil {
		return managementusecase.NodeConfigDocument{}, err
	}
	if a == nil {
		return managementusecase.NodeConfigDocument{}, managementusecase.ErrNodeConfigUnavailable
	}
	nodeID := a.cfg.Cluster.NodeID
	if nodeID == 0 {
		nodeID = a.cfg.NodeID
	}
	if nodeID != requestedNodeID {
		return managementusecase.NodeConfigDocument{}, managementusecase.ErrNodeConfigUnavailable
	}
	document := a.cfg.StartupConfigDocument
	if err := document.Validate(requestedNodeID); err != nil {
		return managementusecase.NodeConfigDocument{}, err
	}
	return document.Clone(), nil
}
