package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const defaultGatewayTokenLookupTimeout = 3 * time.Second

var errGatewayTokenRejected = errors.New("internal/app: gateway token rejected")

// gatewayTokenMetadataReader loads the authoritative per-device token from the
// current UID Slot route. Implementations must not fall back to stale local data.
type gatewayTokenMetadataReader interface {
	GetDevice(context.Context, string, int64) (metadb.Device, error)
}

// newGatewayTokenVerifier creates the fail-closed verifier injected into the
// reusable gateway authenticator. Returned errors never contain token material.
func newGatewayTokenVerifier(
	reader gatewayTokenMetadataReader,
	timeout time.Duration,
) func(context.Context, string, frame.DeviceFlag, string) (frame.DeviceLevel, error) {
	verifyCredential := newGatewayCredentialVerifier(reader, timeout)
	return func(ctx context.Context, uid string, deviceFlag frame.DeviceFlag, token string) (frame.DeviceLevel, error) {
		result, err := verifyCredential(ctx, uid, deviceFlag, token)
		return result.DeviceLevel, err
	}
}

// newGatewayCredentialVerifier returns the complete durable admission fence.
func newGatewayCredentialVerifier(
	reader gatewayTokenMetadataReader,
	timeout time.Duration,
) func(context.Context, string, frame.DeviceFlag, string) (gateway.CredentialAuthResult, error) {
	if timeout <= 0 {
		timeout = defaultGatewayTokenLookupTimeout
	}
	return func(
		parent context.Context,
		uid string,
		deviceFlag frame.DeviceFlag,
		token string,
	) (gateway.CredentialAuthResult, error) {
		if reader == nil || parent == nil || !validGatewayAuthIdentity(uid, token) {
			return gateway.CredentialAuthResult{}, errGatewayTokenRejected
		}
		if !validGatewayDeviceFlag(deviceFlag) {
			return gateway.CredentialAuthResult{}, errGatewayTokenRejected
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()

		device, err := reader.GetDevice(ctx, uid, int64(deviceFlag))
		if err != nil || device.UID != uid || device.DeviceFlag != int64(deviceFlag) {
			return gateway.CredentialAuthResult{}, errGatewayTokenRejected
		}
		if device.CredentialStatus != metadb.DeviceCredentialStatusActive ||
			device.CredentialVersion == 0 || device.LoginSessionID == "" ||
			device.ExpiresAtUnixMS <= time.Now().UnixMilli() ||
			!constantTimeTokenEqual(device.Token, token) {
			return gateway.CredentialAuthResult{}, errGatewayTokenRejected
		}
		level := frame.DeviceLevel(device.DeviceLevel)
		if level != frame.DeviceLevelSlave && level != frame.DeviceLevelMaster {
			return gateway.CredentialAuthResult{}, errGatewayTokenRejected
		}
		return gateway.CredentialAuthResult{
			DeviceLevel: level, CredentialVersion: device.CredentialVersion,
			LoginSessionID: device.LoginSessionID, ExpiresAtUnixMS: device.ExpiresAtUnixMS,
		}, nil
	}
}

func validGatewayAuthIdentity(uid, token string) bool {
	return uid != "" && uid == strings.TrimSpace(uid) &&
		token != "" && token == strings.TrimSpace(token)
}

func validGatewayDeviceFlag(flag frame.DeviceFlag) bool {
	return flag == frame.APP || flag == frame.WEB || flag == frame.PC
}

func constantTimeTokenEqual(stored, supplied string) bool {
	storedDigest := sha256.Sum256([]byte(stored))
	suppliedDigest := sha256.Sum256([]byte(supplied))
	return subtle.ConstantTimeCompare(storedDigest[:], suppliedDigest[:]) == 1
}
