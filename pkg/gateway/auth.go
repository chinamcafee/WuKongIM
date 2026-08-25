package gateway

import (
	"context"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

type Authenticator = gatewaytypes.Authenticator
type AuthenticatorFunc = gatewaytypes.AuthenticatorFunc
type AuthResult = gatewaytypes.AuthResult
type CredentialAuthResult = gatewaytypes.CredentialAuthResult

const (
	SessionValueUID                 = gatewaytypes.SessionValueUID
	SessionValueDeviceID            = gatewaytypes.SessionValueDeviceID
	SessionValueDeviceFlag          = gatewaytypes.SessionValueDeviceFlag
	SessionValueDeviceLevel         = gatewaytypes.SessionValueDeviceLevel
	SessionValueProtocolVersion     = gatewaytypes.SessionValueProtocolVersion
	SessionValueEncryptionEnabled   = gatewaytypes.SessionValueEncryptionEnabled
	SessionValueAESKey              = gatewaytypes.SessionValueAESKey
	SessionValueAESIV               = gatewaytypes.SessionValueAESIV
	SessionValueCrypto              = gatewaytypes.SessionValueCrypto
	SessionValueCredentialVersion   = gatewaytypes.SessionValueCredentialVersion
	SessionValueLoginSessionID      = gatewaytypes.SessionValueLoginSessionID
	SessionValueCredentialExpiresAt = gatewaytypes.SessionValueCredentialExpiresAt
)

type WKProtoAuthOptions struct {
	TokenAuthOn       bool
	EncryptionEnabled bool
	DisableEncryption bool
	// RequiredProtocolVersion rejects every other CONNECT version when non-zero.
	RequiredProtocolVersion uint8
	NodeID                  uint64
	Now                     func() time.Time

	IsVisitor   func(uid string) bool
	VerifyToken func(ctx context.Context, uid string, deviceFlag frame.DeviceFlag, token string) (frame.DeviceLevel, error)
	// VerifyCredential returns the complete durable admission fence. It takes precedence over VerifyToken.
	VerifyCredential func(ctx context.Context, uid string, deviceFlag frame.DeviceFlag, token string) (CredentialAuthResult, error)
	IsBanned         func(uid string) (bool, error)
}

func NewWKProtoAuthenticator(opts WKProtoAuthOptions) Authenticator {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	encryptionEnabled := true
	if opts.DisableEncryption {
		encryptionEnabled = false
	}
	if opts.EncryptionEnabled {
		encryptionEnabled = true
	}

	return AuthenticatorFunc(func(authContext *Context, connect *frame.ConnectPacket) (*AuthResult, error) {
		if connect == nil {
			return &AuthResult{
				Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail},
			}, nil
		}
		if opts.RequiredProtocolVersion != 0 && connect.Version != opts.RequiredProtocolVersion {
			return &AuthResult{
				Connack: connackForConnect(connect, opts.RequiredProtocolVersion, frame.ReasonProtocolUpgradeRequired),
			}, nil
		}

		deviceLevel := frame.DeviceLevelSlave
		var credentialVersion uint64
		var loginSessionID string
		var credentialExpiresAt int64
		if opts.TokenAuthOn && !isVisitor(opts.IsVisitor, connect.UID) {
			if connect.Token == "" || (opts.VerifyCredential == nil && opts.VerifyToken == nil) {
				return &AuthResult{
					Connack: connackForConnect(connect, opts.RequiredProtocolVersion, frame.ReasonAuthFail),
				}, nil
			}

			ctx := context.Background()
			if authContext != nil && authContext.RequestContext != nil {
				ctx = authContext.RequestContext
			}
			var err error
			if opts.VerifyCredential != nil {
				credential, verifyErr := opts.VerifyCredential(ctx, connect.UID, connect.DeviceFlag, connect.Token)
				err = verifyErr
				deviceLevel = credential.DeviceLevel
				credentialVersion = credential.CredentialVersion
				loginSessionID = credential.LoginSessionID
				credentialExpiresAt = credential.ExpiresAtUnixMS
			} else {
				deviceLevel, err = opts.VerifyToken(ctx, connect.UID, connect.DeviceFlag, connect.Token)
			}
			if err != nil {
				return &AuthResult{
					Connack: connackForConnect(connect, opts.RequiredProtocolVersion, frame.ReasonAuthFail),
				}, nil
			}
		}

		if opts.IsBanned != nil {
			banned, err := opts.IsBanned(connect.UID)
			if err != nil || banned {
				reason := frame.ReasonBan
				if err != nil {
					reason = frame.ReasonAuthFail
				}
				return &AuthResult{
					Connack: connackForConnect(connect, opts.RequiredProtocolVersion, reason),
				}, nil
			}
		}

		connack := connackForConnect(connect, opts.RequiredProtocolVersion, frame.ReasonSuccess)
		connack.TimeDiff = nowFn().UnixMilli() - connect.ClientTimestamp
		connack.NodeId = opts.NodeID
		serverVersion := connack.ServerVersion

		sessionValues := map[string]any{
			SessionValueUID:             connect.UID,
			SessionValueDeviceID:        connect.DeviceID,
			SessionValueDeviceFlag:      connect.DeviceFlag,
			SessionValueDeviceLevel:     deviceLevel,
			SessionValueProtocolVersion: serverVersion,
		}
		if credentialVersion != 0 {
			sessionValues[SessionValueCredentialVersion] = credentialVersion
			sessionValues[SessionValueLoginSessionID] = loginSessionID
			sessionValues[SessionValueCredentialExpiresAt] = credentialExpiresAt
		}
		if encryptionEnabled {
			if connect.ClientKey == "" {
				return &AuthResult{
					Connack: connackForConnect(connect, opts.RequiredProtocolVersion, frame.ReasonClientKeyIsEmpty),
				}, nil
			}
			sessionKeys, serverKey, err := wkprotoenc.NegotiateServerSession(connect.ClientKey)
			if err != nil {
				return &AuthResult{
					Connack: connackForConnect(connect, opts.RequiredProtocolVersion, frame.ReasonAuthFail),
				}, nil
			}
			sessionCrypto, err := wkprotoenc.NewSessionCrypto(sessionKeys)
			if err != nil {
				return &AuthResult{
					Connack: connackForConnect(connect, opts.RequiredProtocolVersion, frame.ReasonAuthFail),
				}, nil
			}
			connack.ServerKey = serverKey
			connack.Salt = string(sessionKeys.AESIV)
			sessionValues[SessionValueEncryptionEnabled] = true
			sessionValues[SessionValueAESKey] = sessionKeys.AESKey
			sessionValues[SessionValueAESIV] = sessionKeys.AESIV
			sessionValues[SessionValueCrypto] = sessionCrypto
		}

		return &AuthResult{
			Connack:       connack,
			SessionValues: sessionValues,
		}, nil
	})
}

// connackForConnect preserves the negotiated protocol envelope on every
// response, including authentication failures. V4+ clients need the version
// flag to decode the reason code instead of mistaking a valid failure for a
// legacy CONNACK payload.
func connackForConnect(connect *frame.ConnectPacket, requiredVersion uint8, reason frame.ReasonCode) *frame.ConnackPacket {
	connack := &frame.ConnackPacket{ReasonCode: reason}
	if connect == nil {
		return connack
	}

	serverVersion := connect.Version
	if requiredVersion != 0 {
		serverVersion = requiredVersion
	} else if serverVersion == 0 || serverVersion > frame.LatestVersion {
		serverVersion = frame.LatestVersion
	}
	connack.ServerVersion = serverVersion
	connack.HasServerVersion = connect.Version > 3 || requiredVersion != 0
	return connack
}

func isVisitor(fn func(uid string) bool, uid string) bool {
	if fn == nil {
		return false
	}
	return fn(uid)
}
