package gateway_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestAuthenticatorRequiresLatestProtocolVersion(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		DisableEncryption:       true,
		RequiredProtocolVersion: frame.LatestVersion,
	})

	for _, version := range []uint8{0, 5, frame.LatestVersion + 1} {
		result, err := auth.Authenticate(nil, &frame.ConnectPacket{Version: version, UID: "u1"})
		if err != nil {
			t.Fatalf("Authenticate(version=%d) error = %v", version, err)
		}
		if result.Connack.ReasonCode != frame.ReasonProtocolUpgradeRequired {
			t.Fatalf("Authenticate(version=%d) reason = %v, want %v", version, result.Connack.ReasonCode, frame.ReasonProtocolUpgradeRequired)
		}
		if result.Connack.ServerVersion != frame.LatestVersion || !result.Connack.HasServerVersion {
			t.Fatalf("Authenticate(version=%d) connack = %#v, want latest version hint", version, result.Connack)
		}
		if len(result.SessionValues) != 0 {
			t.Fatalf("Authenticate(version=%d) created session values: %#v", version, result.SessionValues)
		}
	}

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{Version: frame.LatestVersion, UID: "u1"})
	if err != nil {
		t.Fatalf("Authenticate(latest) error = %v", err)
	}
	if result.Connack.ReasonCode != frame.ReasonSuccess || result.SessionValues[gateway.SessionValueProtocolVersion] != uint8(frame.LatestVersion) {
		t.Fatalf("Authenticate(latest) result = %#v, want v%d success", result, frame.LatestVersion)
	}
}

func TestAuthenticatorStoresDeviceIDSessionValue(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{DisableEncryption: true})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{
		Version:  frame.LatestVersion,
		UID:      "u1",
		DeviceID: "d-1",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.SessionValues[gateway.SessionValueDeviceID] != "d-1" {
		t.Fatalf("device id = %#v, want %q", result.SessionValues[gateway.SessionValueDeviceID], "d-1")
	}
}

func TestAuthenticatorNegotiatesWKProtoEncryption(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		EncryptionEnabled: true,
	})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{
		Version:   frame.LatestVersion,
		UID:       "u1",
		ClientKey: testClientPublicKey(t),
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.Connack.ServerKey == "" {
		t.Fatal("ServerKey is empty")
	}
	if result.Connack.Salt == "" {
		t.Fatal("Salt is empty")
	}
	if got := result.SessionValues[gateway.SessionValueEncryptionEnabled]; got != true {
		t.Fatalf("encryption enabled = %#v, want true", got)
	}
	if _, ok := result.SessionValues[gateway.SessionValueAESKey].([]byte); !ok {
		t.Fatalf("AESKey type = %T, want []byte", result.SessionValues[gateway.SessionValueAESKey])
	}
	if _, ok := result.SessionValues[gateway.SessionValueAESIV].([]byte); !ok {
		t.Fatalf("AESIV type = %T, want []byte", result.SessionValues[gateway.SessionValueAESIV])
	}
	if _, ok := result.SessionValues[gateway.SessionValueCrypto].(*wkprotoenc.SessionCrypto); !ok {
		t.Fatalf("SessionCrypto type = %T, want *wkprotoenc.SessionCrypto", result.SessionValues[gateway.SessionValueCrypto])
	}
}

func TestAuthenticatorRejectsMissingClientKeyWhenEncryptionEnabled(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		EncryptionEnabled: true,
	})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{Version: frame.LatestVersion, UID: "u1"})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got, want := result.Connack.ReasonCode, frame.ReasonClientKeyIsEmpty; got != want {
		t.Fatalf("ReasonCode = %v, want %v", got, want)
	}
}

func TestAuthenticatorRejectsInvalidClientKeyWhenEncryptionEnabled(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		EncryptionEnabled: true,
	})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{
		Version:   frame.LatestVersion,
		UID:       "u1",
		ClientKey: "bad-client-key",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if got, want := result.Connack.ReasonCode, frame.ReasonAuthFail; got != want {
		t.Fatalf("ReasonCode = %v, want %v", got, want)
	}
}

func TestAuthenticatorSkipsEncryptionMaterialWhenDisabled(t *testing.T) {
	auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{
		DisableEncryption: true,
	})

	result, err := auth.Authenticate(nil, &frame.ConnectPacket{
		Version:   frame.LatestVersion,
		UID:       "u1",
		ClientKey: testClientPublicKey(t),
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.Connack.ServerKey != "" {
		t.Fatalf("ServerKey = %q, want empty", result.Connack.ServerKey)
	}
	if result.Connack.Salt != "" {
		t.Fatalf("Salt = %q, want empty", result.Connack.Salt)
	}
	if got := result.SessionValues[gateway.SessionValueEncryptionEnabled]; got != nil {
		t.Fatalf("encryption enabled = %#v, want nil", got)
	}
}

func testClientPublicKey(t *testing.T) string {
	t.Helper()

	_, public, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	return wkprotoenc.EncodePublicKey(public)
}
