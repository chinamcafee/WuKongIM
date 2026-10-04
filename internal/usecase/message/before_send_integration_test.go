//go:build integration

package message

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBeforeSendWebhookDeadlineDecisionPrecedence(t *testing.T) {
	for _, deny := range []bool{false, true} {
		hook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(ctx context.Context, _ BeforeSendRequest) (BeforeSendDecision, error) {
			<-ctx.Done()
			if deny {
				return BeforeSendDecision{ReasonCode: 200}, nil
			}
			return BeforeSendDecision{Allow: true, Payload: []byte("late mutation")}, nil
		}), func(opts *BeforeSendOptions) {
			opts.Timeout = 5 * time.Millisecond
			opts.OnTimeout = "allow"
			opts.OnError = "allow"
		})
		submitter := &recordingSubmitter{}
		app := New(Options{BeforeSendWebhook: hook, Submitter: submitter})
		result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: 2, Payload: []byte("original")})
		require.NoError(t, err)
		if deny {
			require.Equal(t, Reason(200), result.Reason)
			require.Empty(t, submitter.sendCommand.FromUID)
		} else {
			require.Equal(t, ReasonSuccess, result.Reason)
			require.Equal(t, "original", string(submitter.sendCommand.Payload))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	hook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(callCtx context.Context, _ BeforeSendRequest) (BeforeSendDecision, error) {
		<-callCtx.Done()
		return BeforeSendDecision{Allow: true}, nil
	}), func(opts *BeforeSendOptions) { opts.OnTimeout = "allow"; opts.OnError = "allow" })
	submitter := &recordingSubmitter{}
	_, err := New(Options{BeforeSendWebhook: hook, Submitter: submitter}).Send(ctx, SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: 2, Payload: []byte("original")})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, submitter.sendCommand.FromUID)
}
