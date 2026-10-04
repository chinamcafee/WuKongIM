package meta

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLegacyDeviceImportCannotDowngradeSignedCredential(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	s := store.db.HashSlot(1)
	legacy := Device{UID: "u", DeviceFlag: 1, DeviceLevel: 1, Token: "old-token"}
	require.NoError(t, s.UpsertDevice(ctx, legacy))
	wb := store.db.NewBatch()
	defer wb.Close()
	signed := Device{UID: "u", DeviceFlag: 1, DeviceLevel: 1, Token: "new-token", CredentialVersion: 2, LoginSessionID: "session", OperationID: "op", OperationDigest: "digest", CredentialStatus: DeviceCredentialStatusActive, ExpiresAtUnixMS: 2000000000000, UpdatedAtUnixMS: 1900000000000}
	require.NoError(t, s.UpsertDevice(ctx, signed))
	require.Error(t, s.UpsertDevice(ctx, legacy))
	got, ok, err := s.GetDevice(ctx, "u", 1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, signed, got)
	compat := (&DB{meta: store.db, engine: store.engine}).NewWriteBatch()
	defer compat.Close()
	_, err = compat.ApplyDeviceCredentialConditionally(1, legacy)
	require.Error(t, err)
}
