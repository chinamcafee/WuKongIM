package raftlog

import (
	"context"
	"errors"

	"github.com/cockroachdb/pebble/v2"
	"go.etcd.io/raft/v3/raftpb"
)

// InspectOfflineSlotBootstrap reads bounded initial Slot snapshots from stopped
// native storage. It neither starts the write/GC workers nor initializes keys.
// The caller must exclude native writers while consuming the callback.
func InspectOfflineSlotBootstrap(ctx context.Context, path string, slots []uint64, maxBytes uint64, visit func(uint64, raftpb.Snapshot) error) (err error) {
	opts, err := normalizeOptions(path, Options{})
	if err != nil {
		return err
	}
	p, err := pebble.Open(path, &pebble.Options{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	db := &DB{db: p, options: opts, snapshotStore: newSnapshotStore(opts.SnapshotPath, opts.SnapshotChunkSize), activeSnapshotPaths: make(map[string]int)}
	for _, id := range slots {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := &pebbleStore{db: db, scope: SlotScope(id)}
		m, found, err := s.loadMetaFrom(p)
		if err != nil {
			return err
		}
		if !found || m.LastIndex != 1 || m.SnapshotIndex != 1 || m.SnapshotTerm != 1 || m.AppliedIndex > 1 {
			return errors.New("Slot bootstrap metadata differs from initial generation")
		}
		hs, err := s.loadHardState()
		if err != nil {
			return err
		}
		if hs != (raftpb.HardState{Term: 1, Commit: 1}) {
			return errors.New("Slot bootstrap HardState differs from initial generation")
		}
		manifest, found, err := s.loadSnapshotManifest(ctx)
		if err != nil {
			return err
		}
		if !found || manifest.TotalSize > maxBytes {
			return errors.New("missing or oversized Slot bootstrap snapshot")
		}
		snap, err := s.loadSnapshot(ctx)
		if err != nil {
			return err
		}
		if err := visit(id, snap); err != nil {
			return err
		}
	}
	return nil
}
