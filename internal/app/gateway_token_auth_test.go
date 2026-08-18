package app

import (
	"context"
	"errors"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestGatewayTokenVerifierAcceptsAuthoritativeDeviceAndLevel(t *testing.T) {
	reader := &gatewayTokenReaderStub{device: metadb.Device{
		UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-current",
		DeviceLevel: int64(frame.DeviceLevelMaster), CredentialVersion: 7,
		LoginSessionID: "session-7", CredentialStatus: metadb.DeviceCredentialStatusActive,
		ExpiresAtUnixMS: time.Now().Add(time.Hour).UnixMilli(),
	}}
	verify := newGatewayTokenVerifier(reader, time.Second)

	level, err := verify(context.Background(), "u1", frame.APP, "token-current")
	if err != nil {
		t.Fatalf("verify() error = %v", err)
	}
	if level != frame.DeviceLevelMaster {
		t.Fatalf("level = %v, want master", level)
	}
	if reader.uid != "u1" || reader.deviceFlag != int64(frame.APP) {
		t.Fatalf("metadata lookup = %q/%d, want u1/app", reader.uid, reader.deviceFlag)
	}
}

func TestGatewayCredentialVerifierReturnsDurableAdmissionFence(t *testing.T) {
	expires := time.Now().Add(time.Hour).UnixMilli()
	reader := &gatewayTokenReaderStub{device: metadb.Device{
		UID: "u1", DeviceFlag: int64(frame.PC), Token: "desktop-token",
		DeviceLevel: int64(frame.DeviceLevelMaster), CredentialVersion: 11,
		LoginSessionID: "desktop-session", CredentialStatus: metadb.DeviceCredentialStatusActive,
		ExpiresAtUnixMS: expires,
	}}
	verify := newGatewayCredentialVerifier(reader, time.Second)

	result, err := verify(context.Background(), "u1", frame.PC, "desktop-token")
	if err != nil || result.DeviceLevel != frame.DeviceLevelMaster || result.CredentialVersion != 11 ||
		result.LoginSessionID != "desktop-session" || result.ExpiresAtUnixMS != expires {
		t.Fatalf("verify() = %#v, %v, want complete durable admission fence", result, err)
	}
}

func TestGatewayCredentialVerifierRejectsRevokedAndExpiredRows(t *testing.T) {
	reader := &gatewayTokenReaderStub{device: metadb.Device{
		UID: "u1", DeviceFlag: int64(frame.APP), Token: "token",
		DeviceLevel: int64(frame.DeviceLevelMaster), CredentialVersion: 3,
		LoginSessionID: "session-3", CredentialStatus: metadb.DeviceCredentialStatusRevoked,
	}}
	verify := newGatewayCredentialVerifier(reader, time.Second)
	if _, err := verify(context.Background(), "u1", frame.APP, "token"); !errors.Is(err, errGatewayTokenRejected) {
		t.Fatalf("revoked verify error = %v, want rejected", err)
	}
	reader.device.CredentialStatus = metadb.DeviceCredentialStatusActive
	reader.device.ExpiresAtUnixMS = time.Now().Add(-time.Millisecond).UnixMilli()
	if _, err := verify(context.Background(), "u1", frame.APP, "token"); !errors.Is(err, errGatewayTokenRejected) {
		t.Fatalf("expired verify error = %v, want rejected", err)
	}
}

func TestGatewayTokenVerifierRejectsWrongRotatedAndMismatchedDeviceTokens(t *testing.T) {
	reader := &gatewayTokenReaderStub{device: metadb.Device{
		UID: "u1", DeviceFlag: int64(frame.APP), Token: "token-current",
		DeviceLevel: int64(frame.DeviceLevelSlave), CredentialVersion: 7,
		LoginSessionID: "session-7", CredentialStatus: metadb.DeviceCredentialStatusActive,
		ExpiresAtUnixMS: time.Now().Add(time.Hour).UnixMilli(),
	}}
	verify := newGatewayTokenVerifier(reader, time.Second)

	for _, test := range []struct {
		name       string
		uid        string
		deviceFlag frame.DeviceFlag
		token      string
	}{
		{name: "wrong token", uid: "u1", deviceFlag: frame.APP, token: "token-wrong"},
		{name: "rotated old token", uid: "u1", deviceFlag: frame.APP, token: "token-old"},
		{name: "wrong device flag", uid: "u1", deviceFlag: frame.WEB, token: "token-current"},
		{name: "unsupported device flag", uid: "u1", deviceFlag: frame.SYSTEM, token: "token-current"},
		{name: "blank token", uid: "u1", deviceFlag: frame.APP, token: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := verify(context.Background(), test.uid, test.deviceFlag, test.token); !errors.Is(err, errGatewayTokenRejected) {
				t.Fatalf("verify() error = %v, want token rejected", err)
			}
		})
	}

	reader.device.Token = "token-rotated"
	if _, err := verify(context.Background(), "u1", frame.APP, "token-current"); !errors.Is(err, errGatewayTokenRejected) {
		t.Fatalf("old token after rotation error = %v, want token rejected", err)
	}
}

func TestGatewayTokenVerifierFailsClosedOnMetadataErrorTimeoutAndInvalidLevel(t *testing.T) {
	reader := &gatewayTokenReaderStub{err: errors.New("route unavailable")}
	verify := newGatewayTokenVerifier(reader, 20*time.Millisecond)
	if _, err := verify(context.Background(), "u1", frame.APP, "token"); !errors.Is(err, errGatewayTokenRejected) {
		t.Fatalf("metadata error = %v, want token rejected", err)
	}

	reader.err = nil
	reader.waitForContext = true
	if _, err := verify(context.Background(), "u1", frame.APP, "token"); !errors.Is(err, errGatewayTokenRejected) {
		t.Fatalf("metadata timeout = %v, want token rejected", err)
	}

	reader.waitForContext = false
	reader.device = metadb.Device{
		UID: "u1", DeviceFlag: int64(frame.APP), Token: "token", DeviceLevel: 9,
		CredentialVersion: 7, LoginSessionID: "session-7",
		CredentialStatus: metadb.DeviceCredentialStatusActive,
		ExpiresAtUnixMS:  time.Now().Add(time.Hour).UnixMilli(),
	}
	if _, err := verify(context.Background(), "u1", frame.APP, "token"); !errors.Is(err, errGatewayTokenRejected) {
		t.Fatalf("invalid device level = %v, want token rejected", err)
	}
}

func TestWireGatewayRequiresTokenMetadataReaderWhenAuthEnabled(t *testing.T) {
	a := &App{cfg: Config{Gateway: GatewayConfig{
		TokenAuthEnabled: true,
		Listeners: []gateway.ListenerOptions{{
			Name: "tcp", Network: "tcp", Address: "127.0.0.1:0",
		}},
	}}}
	if err := a.wireGateway(1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("wireGateway() error = %v, want invalid config", err)
	}
}

type gatewayTokenReaderStub struct {
	device         metadb.Device
	err            error
	uid            string
	deviceFlag     int64
	waitForContext bool
}

func (s *gatewayTokenReaderStub) GetDevice(
	ctx context.Context,
	uid string,
	deviceFlag int64,
) (metadb.Device, error) {
	s.uid = uid
	s.deviceFlag = deviceFlag
	if s.waitForContext {
		<-ctx.Done()
		return metadb.Device{}, ctx.Err()
	}
	return s.device, s.err
}
