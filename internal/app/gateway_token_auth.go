package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
	if timeout <= 0 {
		timeout = defaultGatewayTokenLookupTimeout
	}
	return func(
		parent context.Context,
		uid string,
		deviceFlag frame.DeviceFlag,
		token string,
	) (frame.DeviceLevel, error) {
		if reader == nil || parent == nil || !validGatewayAuthIdentity(uid, token) {
			return 0, errGatewayTokenRejected
		}
		if !validGatewayDeviceFlag(deviceFlag) {
			return 0, errGatewayTokenRejected
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()

		device, err := reader.GetDevice(ctx, uid, int64(deviceFlag))
		if err != nil || device.UID != uid || device.DeviceFlag != int64(deviceFlag) {
			return 0, errGatewayTokenRejected
		}
		if !constantTimeTokenEqual(device.Token, token) {
			return 0, errGatewayTokenRejected
		}
		level := frame.DeviceLevel(device.DeviceLevel)
		if level != frame.DeviceLevelSlave && level != frame.DeviceLevelMaster {
			return 0, errGatewayTokenRejected
		}
		return level, nil
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
