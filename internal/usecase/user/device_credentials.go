package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	CredentialRouteOutcomeComplete = "COMPLETE"
	CredentialRouteOutcomePending  = "PENDING"
)

var allowedCredentialOperationKinds = map[string]struct{}{
	"LOGIN_TAKEOVER":    {},
	"TOKEN_REFRESH":     {},
	"SESSION_RECONCILE": {},
}

var allowedCredentialReplacementCauses = map[string]struct{}{
	"SAME_DEVICE_FAMILY_LOGIN": {},
	"TOKEN_REFRESH":            {},
	"SESSION_RECONCILE":        {},
}

var allowedCredentialTerminationCauses = map[string]struct{}{
	"SESSION_REPLACED":            {},
	"SESSION_LOGGED_OUT":          {},
	"SESSION_EXPIRED":             {},
	"SESSION_ADMIN_KICKED":        {},
	"SESSION_ACCOUNT_DISABLED":    {},
	"IDENTITY_NO_LONGER_ELIGIBLE": {},
	"IM_CREDENTIAL_EXPIRED":       {},
}

// ApplyDeviceCredentialCommand applies one ACTIVE credential version.
type ApplyDeviceCredentialCommand struct {
	UID               string
	DeviceFlag        protocolmeta.DeviceFlag
	Token             string
	CredentialVersion uint64
	LoginSessionID    string
	ExpiresAtUnixMS   int64
	OperationID       string
	OperationKind     string
	ReplacementCause  string
}

// RevokeDeviceCredentialCommand applies one REVOKED version tombstone.
type RevokeDeviceCredentialCommand struct {
	UID               string
	DeviceFlag        protocolmeta.DeviceFlag
	CredentialVersion uint64
	LoginSessionID    string
	OperationID       string
	TerminationCause  string
}

// DeviceCredentialResult reports independent durable credential and route outcomes.
type DeviceCredentialResult struct {
	UID                    string                                 `json:"uid"`
	DeviceFlag             protocolmeta.DeviceFlag                `json:"deviceFlag"`
	CredentialOutcome      metadb.DeviceCredentialMutationOutcome `json:"credentialOutcome"`
	RouteOutcome           string                                 `json:"routeOutcome"`
	CurrentVersion         uint64                                 `json:"currentVersion"`
	FenceVersion           uint64                                 `json:"fenceVersion"`
	AuthorityFencedRoutes  int                                    `json:"authorityFencedRoutes"`
	AuthorityPendingRoutes int                                    `json:"authorityPendingRoutes"`
	OwnerLocalFencedRoutes int                                    `json:"ownerLocalFencedRoutes"`
	FrameEnqueuedRoutes    int                                    `json:"frameEnqueuedRoutes"`
	TransportFlushedRoutes int                                    `json:"transportFlushedRoutes"`
	PendingRoutes          int                                    `json:"pendingRoutes"`
	ErrorCode              string                                 `json:"errorCode,omitempty"`
}

// ApplyDeviceCredentials applies a bounded collection item-by-item. Each item is independently linearized.
func (a *App) ApplyDeviceCredentials(ctx context.Context, commands []ApplyDeviceCredentialCommand) []DeviceCredentialResult {
	results := make([]DeviceCredentialResult, len(commands))
	for i, command := range commands {
		results[i] = a.applyDeviceCredential(ctx, command)
	}
	return results
}

// RevokeDeviceCredentials applies durable version tombstones item-by-item.
func (a *App) RevokeDeviceCredentials(ctx context.Context, commands []RevokeDeviceCredentialCommand) []DeviceCredentialResult {
	results := make([]DeviceCredentialResult, len(commands))
	for i, command := range commands {
		results[i] = a.revokeDeviceCredential(ctx, command)
	}
	return results
}

func (a *App) applyDeviceCredential(ctx context.Context, command ApplyDeviceCredentialCommand) DeviceCredentialResult {
	base := credentialResult(command.UID, command.DeviceFlag)
	if err := validateApplyDeviceCredential(command); err != nil {
		base.ErrorCode = "INVALID_ARGUMENT"
		return base
	}
	nowMS := time.Now().UnixMilli()
	device := metadb.Device{
		UID: command.UID, DeviceFlag: int64(command.DeviceFlag), Token: command.Token,
		DeviceLevel: int64(protocolmeta.DeviceLevelMaster), CredentialVersion: command.CredentialVersion,
		LoginSessionID: command.LoginSessionID, OperationID: command.OperationID,
		CredentialStatus: metadb.DeviceCredentialStatusActive, ExpiresAtUnixMS: command.ExpiresAtUnixMS,
		UpdatedAtUnixMS: nowMS,
	}
	device.OperationDigest = credentialOperationDigest(device, command.OperationKind, command.ReplacementCause)
	return a.persistDeviceCredential(ctx, base, device)
}

func (a *App) revokeDeviceCredential(ctx context.Context, command RevokeDeviceCredentialCommand) DeviceCredentialResult {
	base := credentialResult(command.UID, command.DeviceFlag)
	if err := validateRevokeDeviceCredential(command); err != nil {
		base.ErrorCode = "INVALID_ARGUMENT"
		return base
	}
	device := metadb.Device{
		UID: command.UID, DeviceFlag: int64(command.DeviceFlag),
		DeviceLevel: int64(protocolmeta.DeviceLevelMaster), CredentialVersion: command.CredentialVersion,
		LoginSessionID: command.LoginSessionID, OperationID: command.OperationID,
		CredentialStatus: metadb.DeviceCredentialStatusRevoked, UpdatedAtUnixMS: time.Now().UnixMilli(),
		TerminationCause: command.TerminationCause,
	}
	device.OperationDigest = credentialOperationDigest(device, "REVOKE", command.TerminationCause)
	return a.persistDeviceCredential(ctx, base, device)
}

func (a *App) persistDeviceCredential(ctx context.Context, result DeviceCredentialResult, device metadb.Device) DeviceCredentialResult {
	if a == nil || a.credentialStore == nil {
		result.ErrorCode = "CREDENTIAL_STORE_UNAVAILABLE"
		return result
	}
	mutation, err := a.credentialStore.ApplyDeviceCredential(ctx, device)
	if err != nil {
		result.ErrorCode = "RETRYABLE_ERROR"
		return result
	}
	result.CredentialOutcome = mutation.Outcome
	result.CurrentVersion = mutation.CurrentVersion
	result.FenceVersion = mutation.CurrentVersion
	if mutation.Outcome != metadb.DeviceCredentialOutcomeApplied && mutation.Outcome != metadb.DeviceCredentialOutcomeIdempotent {
		return result
	}
	if a.credentialFences == nil {
		result.ErrorCode = "PRESENCE_FENCE_UNAVAILABLE"
		return result
	}
	fence := presence.CredentialFence{
		UID: device.UID, DeviceFlag: uint8(device.DeviceFlag), CredentialVersion: device.CredentialVersion,
		LoginSessionID: device.LoginSessionID, Status: presence.CredentialStatus(device.CredentialStatus),
		ExpiresAtUnixMS: device.ExpiresAtUnixMS, MachineReason: credentialMachineReason(device),
	}
	advance, err := a.credentialFences.AdvanceCredentialFence(ctx, fence)
	result.FenceVersion = advance.CurrentVersion
	result.AuthorityFencedRoutes = advance.ActiveFenced
	result.AuthorityPendingRoutes = advance.PendingFenced
	result.OwnerLocalFencedRoutes = advance.OwnerLocalFenced
	result.FrameEnqueuedRoutes = advance.FrameEnqueued
	result.TransportFlushedRoutes = advance.TransportFlushed
	if err != nil {
		result.PendingRoutes = len(advance.Actions)
		result.ErrorCode = "ROUTE_RECONCILE_PENDING"
		return result
	}
	if len(advance.Actions) != 0 {
		result.PendingRoutes = len(advance.Actions)
		result.ErrorCode = "ROUTE_RECONCILE_PENDING"
		return result
	}
	result.RouteOutcome = CredentialRouteOutcomeComplete
	return result
}

func credentialMachineReason(device metadb.Device) string {
	if device.CredentialStatus == metadb.DeviceCredentialStatusActive {
		return "SESSION_REPLACED_SAME_DEVICE_CLASS"
	}
	switch device.TerminationCause {
	case "SESSION_LOGGED_OUT":
		return "SESSION_LOGGED_OUT"
	case "SESSION_EXPIRED":
		return "SESSION_EXPIRED"
	case "SESSION_ADMIN_KICKED":
		return "SESSION_ADMIN_KICKED"
	case "SESSION_ACCOUNT_DISABLED":
		return "SESSION_ACCOUNT_DISABLED"
	case "IDENTITY_NO_LONGER_ELIGIBLE":
		return "IDENTITY_NO_LONGER_ELIGIBLE"
	case "IM_CREDENTIAL_EXPIRED":
		return "CREDENTIAL_EXPIRED"
	default:
		return "SESSION_REPLACED_SAME_DEVICE_CLASS"
	}
}

func credentialResult(uid string, flag protocolmeta.DeviceFlag) DeviceCredentialResult {
	return DeviceCredentialResult{UID: uid, DeviceFlag: flag, RouteOutcome: CredentialRouteOutcomePending}
}

func validateApplyDeviceCredential(command ApplyDeviceCredentialCommand) error {
	if err := validateCredentialIdentity(command.UID, command.DeviceFlag, command.CredentialVersion, command.LoginSessionID, command.OperationID); err != nil {
		return err
	}
	if strings.TrimSpace(command.Token) == "" || command.ExpiresAtUnixMS <= time.Now().UnixMilli() {
		return metadb.ErrInvalidArgument
	}
	if _, ok := allowedCredentialOperationKinds[command.OperationKind]; !ok {
		return metadb.ErrInvalidArgument
	}
	if _, ok := allowedCredentialReplacementCauses[command.ReplacementCause]; !ok {
		return metadb.ErrInvalidArgument
	}
	if (command.OperationKind == "LOGIN_TAKEOVER" && command.ReplacementCause != "SAME_DEVICE_FAMILY_LOGIN") ||
		(command.OperationKind == "TOKEN_REFRESH" && command.ReplacementCause != "TOKEN_REFRESH") ||
		(command.OperationKind == "SESSION_RECONCILE" && command.ReplacementCause != "SESSION_RECONCILE") {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func validateRevokeDeviceCredential(command RevokeDeviceCredentialCommand) error {
	if err := validateCredentialIdentity(command.UID, command.DeviceFlag, command.CredentialVersion, command.LoginSessionID, command.OperationID); err != nil {
		return err
	}
	if _, ok := allowedCredentialTerminationCauses[command.TerminationCause]; !ok {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func validateCredentialIdentity(uid string, flag protocolmeta.DeviceFlag, version uint64, sessionID, operationID string) error {
	if strings.TrimSpace(uid) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(operationID) == "" || version == 0 {
		return metadb.ErrInvalidArgument
	}
	if flag != protocolmeta.DeviceFlagApp && flag != protocolmeta.DeviceFlagPC {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func credentialOperationDigest(device metadb.Device, operationKind, cause string) string {
	tokenDigest := sha256.Sum256([]byte(device.Token))
	payload := fmt.Sprintf("%s\n%d\n%d\n%s\n%s\n%d\n%s\n%s\n%s\n%s",
		device.UID, device.DeviceFlag, device.CredentialVersion, hex.EncodeToString(tokenDigest[:]),
		device.LoginSessionID, device.ExpiresAtUnixMS, device.OperationID, device.CredentialStatus,
		operationKind, cause)
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}
