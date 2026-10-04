package raftstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestWALAppendReadReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	w, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 1 << 20})
	require.NoError(t, err)
	entries := []raftpb.Entry{{Index: 1, Term: 1, Data: []byte("cmd")}}
	hs := raftpb.HardState{Term: 1, Vote: 1, Commit: 1}
	require.NoError(t, w.appendReady(context.Background(), hs, entries, raftpb.SnapshotMetadata{}))
	require.NoError(t, w.close())

	reopened, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 1 << 20})
	require.NoError(t, err)
	defer reopened.close()
	state, err := reopened.replay()
	require.NoError(t, err)
	require.Equal(t, hs, state.HardState)
	require.Equal(t, entries, state.Entries)
}

func TestWALCutsSegmentsAndRecoversCompleteRecords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	w, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 256})
	require.NoError(t, err)
	for i := uint64(1); i <= 20; i++ {
		hs := raftpb.HardState{Term: 1, Vote: 1, Commit: i}
		require.NoError(t, w.appendReady(context.Background(), hs, []raftpb.Entry{{Index: i, Term: 1, Data: bytes.Repeat([]byte{'x'}, 64)}}, raftpb.SnapshotMetadata{}))
	}
	require.NoError(t, w.close())
	files, err := filepath.Glob(filepath.Join(dir, "*.wal"))
	require.NoError(t, err)
	require.Greater(t, len(files), 1)

	tail := files[len(files)-1]
	f, err := os.OpenFile(tail, os.O_RDWR, 0)
	require.NoError(t, err)
	info, err := f.Stat()
	require.NoError(t, err)
	require.NoError(t, f.Truncate(info.Size()-3))
	require.NoError(t, f.Close())

	reopened, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 256})
	require.NoError(t, err)
	defer reopened.close()
	state, err := reopened.replay()
	require.NoError(t, err)
	require.NotEmpty(t, state.Entries)
	require.LessOrEqual(t, state.HardState.Commit, uint64(20))
}

func TestWALReopensAfterPrefixSegmentsArePruned(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	w, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 256})
	require.NoError(t, err)
	for i := uint64(1); i <= 20; i++ {
		hs := raftpb.HardState{Term: 1, Vote: 1, Commit: i}
		require.NoError(t, w.appendReady(context.Background(), hs, []raftpb.Entry{{Index: i, Term: 1, Data: bytes.Repeat([]byte{'x'}, 64)}}, raftpb.SnapshotMetadata{}))
	}
	require.NoError(t, w.appendAppliedIndex(context.Background(), 20))
	require.NoError(t, w.close())
	require.NoError(t, w.releaseBefore(^uint64(0)))
	files, err := filepath.Glob(filepath.Join(dir, "*.wal"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	seq, _, err := parseSegmentName(filepath.Base(files[0]))
	require.NoError(t, err)
	require.Greater(t, seq, uint64(0))

	reopened, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 256})
	require.NoError(t, err)
	state, err := reopened.replay()
	require.NoError(t, err)
	require.Equal(t, uint64(20), state.AppliedIndex)
	require.NoError(t, reopened.appendAppliedIndex(context.Background(), 21))
	require.NoError(t, reopened.close())

	reopenedAgain, err := openWAL(walConfig{Dir: dir, NodeID: 1, SegmentSize: 256})
	require.NoError(t, err)
	defer reopenedAgain.close()
	state, err = reopenedAgain.replay()
	require.NoError(t, err)
	require.Equal(t, uint64(21), state.AppliedIndex)
}
