package app

import (
	"context"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"testing"
)

func TestNewWiresPersonalSendAccountAdmission(t *testing.T) {
	cfg := Config{DataDir: t.TempDir(), API: APIConfig{InternalCredentialHMACSecret: "fixture-personal-send-key-only-at-least32"},
		Message: MessageConfig{PersonalSendAuthorizationURL: "http://127.0.0.1:1/internal/im/personal-send-authorizations"}}
	app, err := newTestApp(t, cfg, WithCluster(&fakeCluster{}), WithGateway(nil))
	if err != nil {
		t.Fatal(err)
	}
	// No network request: malformed ordinary request-scoped sends are rejected by the wired guard.
	result, err := app.messages.Send(context.Background(), message.SendCommand{FromUID: "ordinary", ChannelType: 0, RequestScoped: true})
	if err != nil || result.Reason != message.ReasonInvalidRequest {
		t.Fatalf("account admission not wired: %v %v", result, err)
	}
}
