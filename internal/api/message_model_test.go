package api

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMessageSendReqPersistentClientMsgNoAndWaitValidation(t *testing.T) {
	base := messageSendReq{
		Header:      types.MessageHeader{NoPersist: 0},
		ClientMsgNo: "relation-event-1",
		Payload:     []byte(`{"type":1001}`),
	}
	require.NoError(t, base.Check())

	missingClientMsgNo := base
	missingClientMsgNo.ClientMsgNo = " "
	require.EqualError(t, missingClientMsgNo.Check(), "持久消息的client_msg_no不能为空！")

	nonPersistent := missingClientMsgNo
	nonPersistent.Header.NoPersist = 1
	require.NoError(t, nonPersistent.Check())

	invalidWait := base
	invalidWait.WaitForPersist = 2
	require.EqualError(t, invalidWait.Check(), "wait_for_persist只能为0或1")

	timeoutWithoutWait := base
	timeoutWithoutWait.PersistTimeoutMS = 3000
	require.EqualError(t, timeoutWithoutWait.Check(), "persist_timeout_ms仅在wait_for_persist=1时有效")

	waitNonPersistent := nonPersistent
	waitNonPersistent.ClientMsgNo = "relation-event-2"
	waitNonPersistent.WaitForPersist = 1
	require.EqualError(t, waitNonPersistent.Check(), "非持久消息不能等待持久化回执")
}

func TestNormalizePersistTimeoutBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		timeoutMS   int
		wantTimeout int
		wantError   bool
	}{
		{name: "default", timeoutMS: 0, wantTimeout: defaultPersistTimeoutMS},
		{name: "minimum", timeoutMS: minPersistTimeoutMS, wantTimeout: minPersistTimeoutMS},
		{name: "maximum", timeoutMS: maxPersistTimeoutMS, wantTimeout: maxPersistTimeoutMS},
		{name: "below minimum", timeoutMS: minPersistTimeoutMS - 1, wantError: true},
		{name: "above maximum", timeoutMS: maxPersistTimeoutMS + 1, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := messageSendReq{WaitForPersist: 1, PersistTimeoutMS: tt.timeoutMS}
			err := normalizePersistTimeout(&req)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantTimeout, req.PersistTimeoutMS)
		})
	}

	defaultPath := messageSendReq{PersistTimeoutMS: 0}
	require.NoError(t, normalizePersistTimeout(&defaultPath))
	require.Zero(t, defaultPath.PersistTimeoutMS)
}

func TestPersistWaitForwardURL(t *testing.T) {
	forwardURL, shouldForward := persistWaitForwardURL(2, 1, "http://node-2:5001", "/message/send")
	require.True(t, shouldForward)
	require.Equal(t, "http://node-2:5001/message/send", forwardURL)

	forwardURL, shouldForward = persistWaitForwardURL(1, 1, "http://node-1:5001", "/message/send")
	require.False(t, shouldForward)
	require.Empty(t, forwardURL)
}
