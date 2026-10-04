package gateway

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestBusinessReasonsPreserveWireCodes(t *testing.T) {
	for _, code := range []message.Reason{128, 200, 255} {
		require.Equal(t, frame.ReasonCode(code), mapReason(code))
	}
	require.Equal(t, frame.ReasonSystemError, mapReason(127))
	require.Equal(t, frame.ReasonSuccess, mapReason(message.ReasonSuccess))
}
