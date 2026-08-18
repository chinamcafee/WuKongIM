package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func activateCommandFromContext(ctx *coregateway.Context, now time.Time) (presence.ActivateCommand, error) {
	if ctx == nil || ctx.Session == nil {
		return presence.ActivateCommand{}, ErrUnauthenticatedSession
	}
	uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	if uid == "" {
		return presence.ActivateCommand{}, ErrUnauthenticatedSession
	}

	listener := ctx.Listener
	if listener == "" {
		listener = ctx.Session.Listener()
	}

	return presence.ActivateCommand{
		UID:               uid,
		DeviceID:          deviceIDFromValue(ctx.Session.Value(coregateway.SessionValueDeviceID)),
		DeviceFlag:        deviceFlagFromValue(ctx.Session.Value(coregateway.SessionValueDeviceFlag)),
		DeviceLevel:       deviceLevelFromValue(ctx.Session.Value(coregateway.SessionValueDeviceLevel)),
		CredentialVersion: uint64FromValue(ctx.Session.Value(coregateway.SessionValueCredentialVersion)),
		LoginSessionID:    stringFromValue(ctx.Session.Value(coregateway.SessionValueLoginSessionID)),
		ExpiresAtUnixMS:   int64FromValue(ctx.Session.Value(coregateway.SessionValueCredentialExpiresAt)),
		Listener:          listener,
		ConnectedUnix:     now.Unix(),
		SessionID:         ctx.Session.ID(),
		Session:           gatewayPresenceSession{ctx: ctx},
	}, nil
}

func stringFromValue(value any) string {
	result, _ := value.(string)
	return result
}

func uint64FromValue(value any) uint64 {
	switch v := value.(type) {
	case uint64:
		return v
	case int64:
		return uint64(v)
	case int:
		return uint64(v)
	default:
		return 0
	}
}

func int64FromValue(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case uint64:
		return int64(v)
	case int:
		return int64(v)
	default:
		return 0
	}
}

func deactivateCommandFromContext(ctx *coregateway.Context) presence.DeactivateCommand {
	if ctx == nil || ctx.Session == nil {
		return presence.DeactivateCommand{}
	}
	uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	return presence.DeactivateCommand{
		UID:       uid,
		SessionID: ctx.Session.ID(),
	}
}

func requestContextFromContext(ctx *coregateway.Context) context.Context {
	if ctx == nil || ctx.RequestContext == nil {
		return context.Background()
	}
	return ctx.RequestContext
}

func deviceIDFromValue(value any) string {
	if deviceID, _ := value.(string); deviceID != "" {
		return deviceID
	}
	return ""
}

func deviceFlagFromValue(value any) uint8 {
	switch v := value.(type) {
	case frame.DeviceFlag:
		return uint8(v)
	case uint8:
		return v
	case int:
		return uint8(v)
	case int32:
		return uint8(v)
	case int64:
		return uint8(v)
	default:
		return 0
	}
}

func deviceLevelFromValue(value any) uint8 {
	switch v := value.(type) {
	case frame.DeviceLevel:
		return uint8(v)
	case uint8:
		return v
	case int:
		return uint8(v)
	case int32:
		return uint8(v)
	case int64:
		return uint8(v)
	default:
		return 0
	}
}

// gatewayPresenceSession closes a gateway session for presence conflict actions.
type gatewayPresenceSession struct {
	// ctx carries the concrete session and core close hook captured at activation.
	ctx *coregateway.Context
}

var _ presence.SessionHandle = gatewayPresenceSession{}

// WriteDelivery writes a server-push frame to the concrete gateway session.
func (s gatewayPresenceSession) WriteDelivery(payload any) error {
	f, ok := payload.(frame.Frame)
	if !ok {
		return errors.New("internal/access/gateway: delivery payload must be a frame")
	}
	if s.ctx == nil || s.ctx.Session == nil {
		return session.ErrSessionClosed
	}
	return s.ctx.Session.WriteFrame(f)
}

// CloseSession closes the concrete gateway session using the core close path when present.
func (s gatewayPresenceSession) CloseSession(reason string) error {
	if s.ctx == nil {
		return session.ErrSessionClosed
	}
	var err error
	if reason != "" {
		err = errors.New(reason)
	}
	return s.ctx.CloseSession(coregateway.CloseReasonPolicyViolation, err)
}

// KickSession sends DISCONNECT(ReasonConnectKick) and closes after transport completion or timeout.
func (s gatewayPresenceSession) KickSession(machineReason string, flushTimeout time.Duration) (online.KickSessionResult, error) {
	if s.ctx == nil {
		return online.KickSessionResult{}, session.ErrSessionClosed
	}
	var closeErr error
	if machineReason != "" {
		closeErr = errors.New(machineReason)
	}
	result, err := s.ctx.KickSession(&frame.DisconnectPacket{
		ReasonCode: frame.ReasonConnectKick,
		Reason:     machineReason,
	}, flushTimeout, closeErr)
	return online.KickSessionResult{
		FrameEnqueued: result.FrameEnqueued, TransportFlushed: result.TransportFlushed, HardClosed: result.HardClosed,
	}, err
}

// RemoteAddr returns the client address observed by the concrete gateway session.
func (s gatewayPresenceSession) RemoteAddr() string {
	if s.ctx == nil || s.ctx.Session == nil {
		return ""
	}
	return s.ctx.Session.RemoteAddr()
}

// LocalAddr returns the listener address observed by the concrete gateway session.
func (s gatewayPresenceSession) LocalAddr() string {
	if s.ctx == nil || s.ctx.Session == nil {
		return ""
	}
	return s.ctx.Session.LocalAddr()
}
