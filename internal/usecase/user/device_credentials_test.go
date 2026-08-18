package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type recordingCredentialStore struct {
	devices []metadb.Device
	result  metadb.DeviceCredentialMutationResult
	err     error
}

func (s *recordingCredentialStore) ApplyDeviceCredential(_ context.Context, device metadb.Device) (metadb.DeviceCredentialMutationResult, error) {
	s.devices = append(s.devices, device)
	return s.result, s.err
}

type recordingCredentialFences struct {
	fences []presence.CredentialFence
	result presence.CredentialFenceAdvanceResult
	err    error
}

func (f *recordingCredentialFences) AdvanceCredentialFence(_ context.Context, fence presence.CredentialFence) (presence.CredentialFenceAdvanceResult, error) {
	f.fences = append(f.fences, fence)
	return f.result, f.err
}

func TestApplyDeviceCredentialUsesMasterCASAndStructuredRouteEvidence(t *testing.T) {
	store := &recordingCredentialStore{result: metadb.DeviceCredentialMutationResult{
		Outcome: metadb.DeviceCredentialOutcomeApplied, CurrentVersion: 7,
	}}
	fences := &recordingCredentialFences{result: presence.CredentialFenceAdvanceResult{
		CurrentVersion: 7, ActiveFenced: 2, PendingFenced: 1, OwnerLocalFenced: 3,
		FrameEnqueued: 2, TransportFlushed: 1, HardClosed: 3,
	}}
	app := New(Options{DeviceCredentials: store, CredentialFences: fences})
	expires := time.Now().Add(time.Hour).UnixMilli()

	result := app.ApplyDeviceCredentials(context.Background(), []ApplyDeviceCredentialCommand{{
		UID: "u1", DeviceFlag: protocolmeta.DeviceFlagApp, Token: "secret-token",
		CredentialVersion: 7, LoginSessionID: "session-7", ExpiresAtUnixMS: expires,
		OperationID: "operation-7", OperationKind: "LOGIN_TAKEOVER",
		ReplacementCause: "SAME_DEVICE_FAMILY_LOGIN",
	}})[0]

	if len(store.devices) != 1 || store.devices[0].DeviceLevel != int64(protocolmeta.DeviceLevelMaster) ||
		store.devices[0].CredentialStatus != metadb.DeviceCredentialStatusActive ||
		store.devices[0].OperationDigest == "" {
		t.Fatalf("stored device = %#v, want complete MASTER ACTIVE credential", store.devices)
	}
	if len(fences.fences) != 1 || fences.fences[0].CredentialVersion != 7 || fences.fences[0].LoginSessionID != "session-7" {
		t.Fatalf("advanced fences = %#v, want version 7/session-7", fences.fences)
	}
	if fences.fences[0].MachineReason != "SESSION_REPLACED_SAME_DEVICE_CLASS" {
		t.Fatalf("machine reason = %q, want terminal login takeover", fences.fences[0].MachineReason)
	}
	if result.CredentialOutcome != metadb.DeviceCredentialOutcomeApplied || result.RouteOutcome != CredentialRouteOutcomeComplete ||
		result.AuthorityFencedRoutes != 2 || result.AuthorityPendingRoutes != 1 ||
		result.OwnerLocalFencedRoutes != 3 || result.FrameEnqueuedRoutes != 2 || result.TransportFlushedRoutes != 1 {
		t.Fatalf("result = %#v, want structured complete evidence", result)
	}
}

func TestIdempotentCredentialMutationStillReconcilesAndReportsPending(t *testing.T) {
	store := &recordingCredentialStore{result: metadb.DeviceCredentialMutationResult{
		Outcome: metadb.DeviceCredentialOutcomeIdempotent, CurrentVersion: 9,
	}}
	pendingAction := presence.RouteAction{UID: "u1", SessionID: 10}
	fences := &recordingCredentialFences{
		result: presence.CredentialFenceAdvanceResult{CurrentVersion: 9, Actions: []presence.RouteAction{pendingAction}},
		err:    errors.New("owner unavailable"),
	}
	app := New(Options{DeviceCredentials: store, CredentialFences: fences})

	result := app.ApplyDeviceCredentials(context.Background(), []ApplyDeviceCredentialCommand{{
		UID: "u1", DeviceFlag: protocolmeta.DeviceFlagPC, Token: "desktop-token",
		CredentialVersion: 9, LoginSessionID: "desktop-session", ExpiresAtUnixMS: time.Now().Add(time.Hour).UnixMilli(),
		OperationID: "operation-9", OperationKind: "TOKEN_REFRESH", ReplacementCause: "TOKEN_REFRESH",
	}})[0]

	if len(fences.fences) != 1 {
		t.Fatalf("fence calls = %d, want idempotent mutation to reconcile", len(fences.fences))
	}
	if fences.fences[0].MachineReason != "CREDENTIAL_ROTATED" {
		t.Fatalf("machine reason = %q, want recoverable credential rotation", fences.fences[0].MachineReason)
	}
	if result.CredentialOutcome != metadb.DeviceCredentialOutcomeIdempotent ||
		result.RouteOutcome != CredentialRouteOutcomePending || result.PendingRoutes != 1 ||
		result.ErrorCode != "ROUTE_RECONCILE_PENDING" {
		t.Fatalf("result = %#v, want IDEMPOTENT + PENDING", result)
	}
}

func TestRevokeDeviceCredentialWritesVersionedTombstone(t *testing.T) {
	store := &recordingCredentialStore{result: metadb.DeviceCredentialMutationResult{
		Outcome: metadb.DeviceCredentialOutcomeApplied, CurrentVersion: 12,
	}}
	fences := &recordingCredentialFences{result: presence.CredentialFenceAdvanceResult{CurrentVersion: 12}}
	app := New(Options{DeviceCredentials: store, CredentialFences: fences})

	result := app.RevokeDeviceCredentials(context.Background(), []RevokeDeviceCredentialCommand{{
		UID: "u1", DeviceFlag: protocolmeta.DeviceFlagApp, CredentialVersion: 12,
		LoginSessionID: "mobile-session", OperationID: "logout-12", TerminationCause: "SESSION_LOGGED_OUT",
	}})[0]

	if result.RouteOutcome != CredentialRouteOutcomeComplete || len(store.devices) != 1 {
		t.Fatalf("result/store = %#v/%#v, want complete tombstone", result, store.devices)
	}
	device := store.devices[0]
	if device.Token != "" || device.ExpiresAtUnixMS != 0 || device.CredentialStatus != metadb.DeviceCredentialStatusRevoked ||
		device.CredentialVersion != 12 || device.TerminationCause != "SESSION_LOGGED_OUT" {
		t.Fatalf("stored device = %#v, want versioned revoked tombstone", device)
	}
}

func TestApplyDeviceCredentialRejectsCallerControlledPolicy(t *testing.T) {
	store := &recordingCredentialStore{}
	app := New(Options{DeviceCredentials: store})
	base := ApplyDeviceCredentialCommand{
		UID: "u1", DeviceFlag: protocolmeta.DeviceFlagApp, Token: "token", CredentialVersion: 1,
		LoginSessionID: "session", ExpiresAtUnixMS: time.Now().Add(time.Hour).UnixMilli(),
		OperationID: "operation", OperationKind: "LOGIN_TAKEOVER", ReplacementCause: "SAME_DEVICE_FAMILY_LOGIN",
	}
	tests := []ApplyDeviceCredentialCommand{
		func() ApplyDeviceCredentialCommand {
			value := base
			value.DeviceFlag = protocolmeta.DeviceFlagSystem
			return value
		}(),
		func() ApplyDeviceCredentialCommand {
			value := base
			value.DeviceFlag = protocolmeta.DeviceFlag(255)
			return value
		}(),
		func() ApplyDeviceCredentialCommand {
			value := base
			value.OperationKind = "CALLER_POLICY"
			return value
		}(),
		func() ApplyDeviceCredentialCommand {
			value := base
			value.ReplacementCause = "SESSION_ADMIN_KICKED"
			return value
		}(),
	}
	for _, command := range tests {
		result := app.ApplyDeviceCredentials(context.Background(), []ApplyDeviceCredentialCommand{command})[0]
		if result.ErrorCode != "INVALID_ARGUMENT" {
			t.Fatalf("command %#v result = %#v, want INVALID_ARGUMENT", command, result)
		}
	}
	if len(store.devices) != 0 {
		t.Fatalf("invalid commands reached store: %#v", store.devices)
	}
}

func TestApplyDeviceCredentialAcceptsFutureTerminalFamilyFlag(t *testing.T) {
	store := &recordingCredentialStore{result: metadb.DeviceCredentialMutationResult{
		Outcome: metadb.DeviceCredentialOutcomeApplied, CurrentVersion: 3,
	}}
	fences := &recordingCredentialFences{result: presence.CredentialFenceAdvanceResult{CurrentVersion: 3}}
	app := New(Options{DeviceCredentials: store, CredentialFences: fences})

	result := app.ApplyDeviceCredentials(context.Background(), []ApplyDeviceCredentialCommand{{
		UID: "u-future", DeviceFlag: protocolmeta.DeviceFlag(7), Token: "future-token",
		CredentialVersion: 3, LoginSessionID: "future-session",
		ExpiresAtUnixMS: time.Now().Add(time.Hour).UnixMilli(), OperationID: "future-operation",
		OperationKind: "SESSION_RECONCILE", ReplacementCause: "SESSION_RECONCILE",
	}})[0]

	if result.ErrorCode != "" || result.RouteOutcome != CredentialRouteOutcomeComplete || len(store.devices) != 1 {
		t.Fatalf("result/store = %#v/%#v, want extensible device flag accepted", result, store.devices)
	}
	if len(fences.fences) != 1 || fences.fences[0].DeviceFlag != 7 ||
		fences.fences[0].MachineReason != "CREDENTIAL_RECONCILED" {
		t.Fatalf("fences = %#v, want future flag reconcile", fences.fences)
	}
}
