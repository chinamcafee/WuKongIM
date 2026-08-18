package gateway

import (
	"testing"
	"time"

	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestGatewayPresenceKickEmitsProtocolDisconnectBeforeBoundedHardClose(t *testing.T) {
	var control frame.Frame
	var timeout time.Duration
	var closeErr error
	ctx := &coregateway.Context{KickSessionFn: func(got frame.Frame, gotTimeout time.Duration, gotErr error) (gatewaytypes.KickResult, error) {
		control = got
		timeout = gotTimeout
		closeErr = gotErr
		return gatewaytypes.KickResult{FrameEnqueued: true, TransportFlushed: true, HardClosed: true}, nil
	}}

	result, err := (gatewayPresenceSession{ctx: ctx}).KickSession(
		"SESSION_REPLACED_SAME_DEVICE_CLASS", 250*time.Millisecond)

	if err != nil {
		t.Fatalf("KickSession() error = %v", err)
	}
	packet, ok := control.(*frame.DisconnectPacket)
	if !ok || packet.GetFrameType() != frame.DISCONNECT || packet.ReasonCode != frame.ReasonConnectKick ||
		packet.Reason != "SESSION_REPLACED_SAME_DEVICE_CLASS" {
		t.Fatalf("control = %#v, want DISCONNECT/ReasonConnectKick/stable machine reason", control)
	}
	if timeout != 250*time.Millisecond {
		t.Fatalf("timeout = %s, want 250ms", timeout)
	}
	if closeErr == nil || closeErr.Error() != "SESSION_REPLACED_SAME_DEVICE_CLASS" {
		t.Fatalf("close error = %v, want stable machine reason", closeErr)
	}
	if !result.FrameEnqueued || !result.TransportFlushed || !result.HardClosed {
		t.Fatalf("result = %#v, want all ordered kick stages observed", result)
	}
}
