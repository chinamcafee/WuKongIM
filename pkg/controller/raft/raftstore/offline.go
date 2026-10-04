package raftstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"go.etcd.io/raft/v3/raftpb"
)

// InspectOfflineBootstrap checks a stopped, freshly initialized Controller WAL
// without creating files, repairing a tail, or updating applied metadata. The
// caller must exclude the native writer for the entire inspection.
func InspectOfflineBootstrap(ctx context.Context, cfg Config) (raftpb.Snapshot, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return raftpb.Snapshot{}, err
	}
	bounded := func(path string, max int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > max {
			return errors.New("invalid offline Controller bootstrap file")
		}
		return nil
	}
	metaPath := filepath.Join(cfg.Dir, "meta.json")
	if err := bounded(metaPath, 1<<20); err != nil {
		return raftpb.Snapshot{}, err
	}
	m, err := loadMetadata(metaPath)
	if err != nil {
		return raftpb.Snapshot{}, err
	}
	expectedPath := filepath.Join(cfg.Dir, "snap", snapshotFileName(1, 1))
	if m.NodeID != cfg.NodeID || m.Version != metadataVersion || m.AppliedIndex != 1 || m.HardState != (raftpb.HardState{Term: 1, Commit: 1}) || m.Snapshot.Index != 1 || m.Snapshot.Term != 1 || m.Snapshot.Path != expectedPath {
		return raftpb.Snapshot{}, errors.New("Controller bootstrap metadata differs from initial generation")
	}
	if err := bounded(expectedPath, 16<<20); err != nil {
		return raftpb.Snapshot{}, err
	}
	snap, err := loadSnapshotFile(expectedPath)
	if err != nil {
		return raftpb.Snapshot{}, err
	}
	files, err := walSegmentFiles(filepath.Join(cfg.Dir, "wal"))
	if err != nil {
		return raftpb.Snapshot{}, err
	}
	if len(files) != 1 {
		return raftpb.Snapshot{}, errors.New("offline Controller bootstrap must have one bounded WAL segment")
	}
	if err := bounded(files[0], 4<<20); err != nil {
		return raftpb.Snapshot{}, err
	}
	w := &wal{cfg: walConfig{Dir: filepath.Join(cfg.Dir, "wal"), NodeID: cfg.NodeID}}
	replayed, err := w.replayMode(false)
	if err != nil {
		return raftpb.Snapshot{}, err
	}
	if replayed.HardState != m.HardState || replayed.AppliedIndex != 1 || len(replayed.Entries) != 0 || !reflect.DeepEqual(replayed.Snapshot, snap.Metadata) || !reflect.DeepEqual(m.ConfState, snap.Metadata.ConfState) {
		return raftpb.Snapshot{}, errors.New("Controller bootstrap WAL and snapshot disagree")
	}
	return snap, nil
}
