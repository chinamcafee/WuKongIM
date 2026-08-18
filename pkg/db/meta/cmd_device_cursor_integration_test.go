//go:build integration

package meta

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCMDDeviceCursorIsFlagIsolatedMonotonicAndRestartSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmd-device-cursor")
	ctx := context.Background()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	shard := db.MetaDB().HashSlot(9)
	for _, cursor := range []CMDDeviceCursor{
		{UID: "u1", DeviceFlag: 0, ChannelID: "g1____cmd", ChannelType: 2,
			ReadSeq: 7, DeletedToSeq: 3, ActiveAt: 100, UpdatedAt: 100},
		{UID: "u1", DeviceFlag: 2, ChannelID: "g1____cmd", ChannelType: 2,
			ReadSeq: 2, ActiveAt: 90, UpdatedAt: 90},
	} {
		if err := shard.UpsertCMDDeviceCursor(ctx, cursor); err != nil {
			t.Fatalf("UpsertCMDDeviceCursor(%+v): %v", cursor, err)
		}
	}
	if err := shard.UpsertCMDDeviceCursor(ctx, CMDDeviceCursor{
		UID: "u1", DeviceFlag: 0, ChannelID: "g1____cmd", ChannelType: 2,
		ReadSeq: 1, DeletedToSeq: 1, ActiveAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("regressing UpsertCMDDeviceCursor(): %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	for flag, wantRead := range map[int64]uint64{0: 7, 2: 2} {
		got, ok, err := db.MetaDB().HashSlot(9).GetCMDDeviceCursor(ctx, CMDDeviceCursorKey{
			UID: "u1", DeviceFlag: flag, ChannelID: "g1____cmd", ChannelType: 2,
		})
		if err != nil || !ok || got.ReadSeq != wantRead {
			t.Fatalf("flag %d cursor = %+v ok=%v err=%v, want read %d", flag, got, ok, err, wantRead)
		}
		if flag == 0 && (got.DeletedToSeq != 3 || got.ActiveAt != 100 || got.UpdatedAt != 100) {
			t.Fatalf("APP cursor regressed across restart: %+v", got)
		}
	}
}

func TestCMDDeviceCursorBatchUsesReadYourWritesMonotonicMerge(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	batch := store.db.NewBatch()
	first := CMDDeviceCursor{UID: "u1", DeviceFlag: 0, ChannelID: "g1____cmd", ChannelType: 2,
		ReadSeq: 9, ActiveAt: 20, UpdatedAt: 20}
	if err := batch.UpsertCMDDeviceCursor(4, first); err != nil {
		t.Fatalf("first staged cursor: %v", err)
	}
	if err := batch.UpsertCMDDeviceCursor(4, CMDDeviceCursor{UID: "u1", DeviceFlag: 0,
		ChannelID: "g1____cmd", ChannelType: 2, ReadSeq: 3, DeletedToSeq: 8,
		ActiveAt: 10, UpdatedAt: 30}); err != nil {
		t.Fatalf("second staged cursor: %v", err)
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	got, ok, err := store.db.HashSlot(4).GetCMDDeviceCursor(ctx, CMDDeviceCursorKey{
		UID: "u1", DeviceFlag: 0, ChannelID: "g1____cmd", ChannelType: 2,
	})
	if err != nil || !ok || got.ReadSeq != 9 || got.DeletedToSeq != 8 || got.ActiveAt != 20 || got.UpdatedAt != 30 {
		t.Fatalf("merged cursor = %+v ok=%v err=%v", got, ok, err)
	}
}
