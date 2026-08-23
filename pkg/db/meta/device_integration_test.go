//go:build integration

package meta

import (
	"context"
	"fmt"
	"testing"
)

func testActiveDevice(uid string, flag int64, token string, level int64, version uint64) Device {
	return Device{
		UID: uid, DeviceFlag: flag, Token: token, DeviceLevel: level,
		CredentialVersion: version, LoginSessionID: fmt.Sprintf("session-%d", version),
		OperationID: fmt.Sprintf("operation-%d", version), OperationDigest: fmt.Sprintf("digest-%d", version),
		CredentialStatus: DeviceCredentialStatusActive, ExpiresAtUnixMS: 2000000000000,
		UpdatedAtUnixMS: 1900000000000,
	}
}

func TestDeviceUpsertAndGetAreHashSlotScoped(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	left := store.db.HashSlot(1)
	right := store.db.HashSlot(2)

	device := testActiveDevice("u1", 1, "left", 3, 1)
	if err := left.UpsertDevice(context.Background(), device); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}
	got, ok, err := left.GetDevice(context.Background(), "u1", 1)
	if err != nil || !ok || got != device {
		t.Fatalf("left GetDevice() = (%+v, %v, %v), want %+v", got, ok, err, device)
	}
	if _, ok, err := right.GetDevice(context.Background(), "u1", 1); err != nil || ok {
		t.Fatalf("right GetDevice() = ok %v err %v, want missing", ok, err)
	}

	updated := testActiveDevice("u1", 1, "new", 4, 2)
	if err := left.UpsertDevice(context.Background(), updated); err != nil {
		t.Fatalf("UpsertDevice(update): %v", err)
	}
	got, ok, err = left.GetDevice(context.Background(), "u1", 1)
	if err != nil || !ok || got != updated {
		t.Fatalf("updated GetDevice() = (%+v, %v, %v), want %+v", got, ok, err, updated)
	}
}

func TestDeviceTableRuntimeDescriptor(t *testing.T) {
	if deviceTable.Schema().ID != TableIDDevice {
		t.Fatalf("device table id = %d, want %d", deviceTable.Schema().ID, TableIDDevice)
	}
	if got := deviceTable.Schema().Primary.Columns; len(got) != 2 {
		t.Fatalf("device primary columns = %#v, want uid and device flag", got)
	}
}

func TestDeviceCredentialConditionalMutationFencesVersionsAndIdempotency(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	apply := func(device Device) DeviceCredentialMutationResult {
		batch := db.NewWriteBatch()
		result, err := batch.ApplyDeviceCredentialConditionally(7, device)
		if err != nil {
			t.Fatalf("ApplyDeviceCredentialConditionally(): %v", err)
		}
		if err := batch.Commit(); err != nil {
			t.Fatalf("Commit(): %v", err)
		}
		return *result
	}

	v2 := testActiveDevice("u1", 1, "token-v2", 1, 2)
	if got := apply(v2); got.Outcome != DeviceCredentialOutcomeApplied || got.CurrentVersion != 2 {
		t.Fatalf("apply v2 = %#v, want APPLIED/2", got)
	}
	if got := apply(v2); got.Outcome != DeviceCredentialOutcomeIdempotent || got.CurrentVersion != 2 {
		t.Fatalf("replay v2 = %#v, want IDEMPOTENT/2", got)
	}
	conflict := v2
	conflict.Token = "different"
	conflict.OperationDigest = "different-digest"
	if got := apply(conflict); got.Outcome != DeviceCredentialOutcomeIdempotencyConflict || got.CurrentVersion != 2 {
		t.Fatalf("conflicting v2 = %#v, want IDEMPOTENCY_CONFLICT/2", got)
	}
	if got := apply(testActiveDevice("u1", 1, "token-v1", 1, 1)); got.Outcome != DeviceCredentialOutcomeStaleVersion || got.CurrentVersion != 2 {
		t.Fatalf("late v1 = %#v, want STALE_VERSION/2", got)
	}
	stored, err := db.ForHashSlot(7).GetDevice(context.Background(), "u1", 1)
	if err != nil || stored != v2 {
		t.Fatalf("stored device = %#v, %v, want v2 unchanged", stored, err)
	}
}

func TestDeviceCredentialVersionsAreIndependentForMobileAndDesktopFlags(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	defer db.Close()
	apply := func(device Device) DeviceCredentialMutationResult {
		batch := db.NewWriteBatch()
		result, err := batch.ApplyDeviceCredentialConditionally(9, device)
		if err != nil {
			t.Fatalf("ApplyDeviceCredentialConditionally(): %v", err)
		}
		if err := batch.Commit(); err != nil {
			t.Fatalf("Commit(): %v", err)
		}
		return *result
	}

	mobileV3 := testActiveDevice("u1", 0, "mobile-v3", 1, 3)
	desktopV5 := testActiveDevice("u1", 2, "desktop-v5", 1, 5)
	if got := apply(mobileV3); got.Outcome != DeviceCredentialOutcomeApplied {
		t.Fatalf("mobile apply = %#v", got)
	}
	if got := apply(desktopV5); got.Outcome != DeviceCredentialOutcomeApplied {
		t.Fatalf("desktop apply = %#v", got)
	}
	if got := apply(testActiveDevice("u1", 0, "mobile-v2", 1, 2)); got.Outcome != DeviceCredentialOutcomeStaleVersion {
		t.Fatalf("late mobile mutation = %#v, want STALE_VERSION", got)
	}

	mobile, err := db.ForHashSlot(9).GetDevice(context.Background(), "u1", 0)
	if err != nil || mobile != mobileV3 {
		t.Fatalf("mobile = %#v, %v, want v3", mobile, err)
	}
	desktop, err := db.ForHashSlot(9).GetDevice(context.Background(), "u1", 2)
	if err != nil || desktop != desktopV5 {
		t.Fatalf("desktop = %#v, %v, want v5 unchanged", desktop, err)
	}
}

func TestDeviceCredentialConditionalMutationUpgradesLegacyDeviceRow(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	const hashSlot HashSlot = 7
	primaryKey, err := deviceTable.primaryRowKey(hashSlot, KeyParts{String("legacy-u1"), Int64Ordered(0)})
	if err != nil {
		t.Fatalf("primaryRowKey(): %v", err)
	}
	legacyValue := appendValueString(nil, "legacy-token")
	legacyValue = appendValueInt64(legacyValue, 1)
	raw := store.engine.NewBatch()
	defer raw.Close()
	if err := raw.Set(primaryKey, legacyValue); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if err := raw.Commit(true); err != nil {
		t.Fatalf("commit legacy row: %v", err)
	}

	legacy, ok, err := store.db.HashSlot(hashSlot).GetDevice(context.Background(), "legacy-u1", 0)
	if err != nil || !ok || legacy.Token != "legacy-token" || legacy.CredentialVersion != 0 {
		t.Fatalf("legacy GetDevice() = (%+v, %v, %v), want compatible version-zero row", legacy, ok, err)
	}

	revoked := Device{
		UID: "legacy-u1", DeviceFlag: 0, DeviceLevel: 1,
		CredentialVersion: 8, LoginSessionID: "session-8", OperationID: "operation-8",
		OperationDigest: "digest-8", CredentialStatus: DeviceCredentialStatusRevoked,
		UpdatedAtUnixMS: 1900000000000, TerminationCause: "SESSION_REPLACED",
	}
	batch := (&DB{meta: store.db, engine: store.engine}).NewWriteBatch()
	defer batch.Close()
	result, err := batch.ApplyDeviceCredentialConditionally(hashSlot, revoked)
	if err != nil {
		t.Fatalf("ApplyDeviceCredentialConditionally(): %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("Commit(): %v", err)
	}
	if result.Outcome != DeviceCredentialOutcomeApplied || result.CurrentVersion != 8 {
		t.Fatalf("result = %#v, want APPLIED/8", result)
	}
	got, ok, err := store.db.HashSlot(hashSlot).GetDevice(context.Background(), "legacy-u1", 0)
	if err != nil || !ok || got != revoked {
		t.Fatalf("upgraded GetDevice() = (%+v, %v, %v), want %+v", got, ok, err, revoked)
	}
}
