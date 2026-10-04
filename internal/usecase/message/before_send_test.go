package message

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	channelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

type beforeSendCallerFunc func(context.Context, BeforeSendRequest) (BeforeSendDecision, error)

func (f beforeSendCallerFunc) BeforeSend(ctx context.Context, req BeforeSendRequest) (BeforeSendDecision, error) {
	return f(ctx, req)
}

func testBeforeSendWebhook(t *testing.T, caller BeforeSendCaller, configure func(*BeforeSendOptions)) *BeforeSendWebhook {
	t.Helper()
	opts := BeforeSendOptions{Caller: caller, Timeout: time.Second, OnTimeout: "deny", OnError: "deny", MaxInFlight: 1, MaxPayloadBytes: 32}
	if configure != nil {
		configure(&opts)
	}
	hook, err := NewBeforeSendWebhook(opts)
	require.NoError(t, err)
	return hook
}

func TestBeforeSendWebhookDecisionsAndFailurePolicies(t *testing.T) {
	tests := []struct {
		name               string
		decision           BeforeSendDecision
		err                error
		onTimeout, onError string
		reason             Reason
		payload, outcome   string
	}{
		{name: "allow", decision: BeforeSendDecision{Allow: true}, payload: "original", outcome: "allow"},
		{name: "replace", decision: BeforeSendDecision{Allow: true, Payload: []byte("changed")}, payload: "changed", outcome: "allow"},
		{name: "deny", reason: ReasonNotAllowSend, outcome: "reject"},
		{name: "business lower", decision: BeforeSendDecision{ReasonCode: 128}, reason: Reason(128), outcome: "reject"},
		{name: "business upper", decision: BeforeSendDecision{ReasonCode: 255}, reason: Reason(255), outcome: "reject"},
		{name: "deny cannot fail open", decision: BeforeSendDecision{ReasonCode: 200}, onError: "allow", onTimeout: "allow", reason: Reason(200), outcome: "reject"},
		{name: "invalid business code", decision: BeforeSendDecision{ReasonCode: 127}, onError: "allow", reason: ReasonNotAllowSend, outcome: "reject"},
		{name: "overflow", decision: BeforeSendDecision{ReasonCode: 256}, onError: "allow", reason: ReasonNotAllowSend, outcome: "reject"},
		{name: "contradictory allow", decision: BeforeSendDecision{Allow: true, ReasonCode: 200}, reason: ReasonSystemError, outcome: "error_deny"},
		{name: "empty replacement", decision: BeforeSendDecision{Allow: true, Payload: []byte{}}, reason: ReasonSystemError, outcome: "error_deny"},
		{name: "oversize replacement", decision: BeforeSendDecision{Allow: true, Payload: make([]byte, 33)}, reason: ReasonSystemError, outcome: "error_deny"},
		{name: "invalid response fail open", decision: BeforeSendDecision{Allow: true, Payload: []byte{}}, onError: "allow", payload: "original", outcome: "error_allow"},
		{name: "transport deny", err: errors.New("transport"), reason: ReasonSystemError, outcome: "error_deny"},
		{name: "transport allow", err: errors.New("transport"), onError: "allow", payload: "original", outcome: "error_allow"},
		{name: "timeout deny despite error allow", err: context.DeadlineExceeded, onError: "allow", reason: ReasonSystemError, outcome: "timeout_deny"},
		{name: "timeout allow", err: context.DeadlineExceeded, onTimeout: "allow", payload: "original", outcome: "timeout_allow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var observed string
			hook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(ctx context.Context, req BeforeSendRequest) (BeforeSendDecision, error) {
				require.Equal(t, "original", string(req.Payload))
				require.Equal(t, "client-id", req.ClientMsgNo)
				_, ok := ctx.Deadline()
				require.True(t, ok)
				return tt.decision, tt.err
			}), func(opts *BeforeSendOptions) {
				if tt.onTimeout != "" {
					opts.OnTimeout = tt.onTimeout
				}
				if tt.onError != "" {
					opts.OnError = tt.onError
				}
				opts.Observe = func(result string, _ time.Duration) { observed = result }
			})
			submitter := &recordingSubmitter{sendResult: SendResult{MessageID: 42, Reason: ReasonSuccess}}
			app := New(Options{Submitter: submitter, BeforeSendWebhook: hook})
			result, err := app.Send(context.Background(), SendCommand{FromUID: "sender", ChannelID: "group", ChannelType: 2, ClientMsgNo: "client-id", Payload: []byte("original")})
			require.NoError(t, err)
			require.Equal(t, tt.reason, result.Reason)
			require.Equal(t, tt.outcome, observed)
			if tt.payload == "" {
				require.Empty(t, submitter.sendCommand.FromUID)
			} else {
				require.Equal(t, tt.payload, string(submitter.sendCommand.Payload))
			}
		})
	}
}

func TestBeforeSendWebhookRunsAfterPluginsAndIgnoresPluginSkip(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "with plugin", true: "skip plugin"}[skip], func(t *testing.T) {
			var got BeforeSendRequest
			hook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(_ context.Context, req BeforeSendRequest) (BeforeSendDecision, error) {
				got = req
				return BeforeSendDecision{Allow: true}, nil
			}), nil)
			plugin := &recordingSendHook{mutate: func(cmd SendCommand) (SendCommand, Reason, error) {
				cmd.Payload = []byte("plugin")
				return cmd, ReasonSuccess, nil
			}}
			submitter := &recordingSubmitter{}
			app := New(Options{Submitter: submitter, SendHook: plugin, BeforeSendWebhook: hook})
			_, err := app.Send(context.Background(), SendCommand{FromUID: "sender", ChannelID: "group", ChannelType: 2, Payload: []byte("original"), SkipPluginHooks: skip, NoPersist: true, SyncOnce: true})
			require.NoError(t, err)
			if skip {
				require.Equal(t, "original", string(got.Payload))
				require.Empty(t, plugin.calls)
			} else {
				require.Equal(t, "plugin", string(got.Payload))
			}
			require.True(t, got.NoPersist)
			require.True(t, got.SyncOnce)
		})
	}
}

func TestBeforeSendWebhookPermissionAndPluginDenialsSkipCallback(t *testing.T) {
	for _, permission := range []bool{false, true} {
		store := newFakePermissionStore()
		if permission {
			store.channels[permissionKey("sender", 1)] = metadb.Channel{SendBan: 1}
		}
		calls := 0
		hook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(context.Context, BeforeSendRequest) (BeforeSendDecision, error) {
			calls++
			return BeforeSendDecision{Allow: true}, nil
		}), nil)
		app := New(Options{PermissionStore: store, BeforeSendWebhook: hook, SendHook: &recordingSendHook{mutate: func(cmd SendCommand) (SendCommand, Reason, error) { return cmd, ReasonNotAllowSend, nil }}})
		result, err := app.Send(context.Background(), SendCommand{FromUID: "sender", ChannelID: "group", ChannelType: 2, Payload: []byte("original")})
		require.NoError(t, err)
		require.NotEqual(t, ReasonSuccess, result.Reason)
		require.Zero(t, calls)
	}
}

func TestBeforeSendWebhookParentCancellationNeverFailsOpen(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		hook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(context.Context, BeforeSendRequest) (BeforeSendDecision, error) {
			calls++
			cancel()
			return BeforeSendDecision{Allow: true, Payload: []byte("late")}, nil
		}), func(opts *BeforeSendOptions) { opts.OnError = "allow"; opts.OnTimeout = "allow" })
		if alreadyCanceled {
			cancel()
		}
		submitter := &recordingSubmitter{}
		app := New(Options{Submitter: submitter, BeforeSendWebhook: hook})
		_, err := app.Send(ctx, SendCommand{FromUID: "sender", ChannelID: "group", ChannelType: 2, Payload: []byte("original")})
		cancel()
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, submitter.sendCommand.FromUID)
		if alreadyCanceled {
			require.Zero(t, calls)
		} else {
			require.Equal(t, 1, calls)
		}
	}
}

func TestBeforeSendWebhookSaturationRejectsWithoutWaitingAndReleasesCapacity(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	hook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(context.Context, BeforeSendRequest) (BeforeSendDecision, error) {
		select {
		case entered <- struct{}{}:
		case <-release:
		}
		<-release
		return BeforeSendDecision{Allow: false}, nil
	}), func(opts *BeforeSendOptions) { opts.OnError = "allow" })
	app := New(Options{BeforeSendWebhook: hook})
	cmd := SendCommand{FromUID: "sender", ChannelID: "group", ChannelType: 2, Payload: []byte("original")}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = app.Send(context.Background(), cmd) }()
	<-entered
	result, err := app.Send(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, ReasonSystemError, result.Reason)
	once.Do(func() { close(release) })
	<-done
	result, err = app.Send(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, ReasonNotAllowSend, result.Reason)
}

func TestBeforeSendWebhookBatchPreservesMixedResults(t *testing.T) {
	calls := 0
	hook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(_ context.Context, req BeforeSendRequest) (BeforeSendDecision, error) {
		calls++
		if string(req.Payload) == "reject" {
			return BeforeSendDecision{ReasonCode: 200}, nil
		}
		return BeforeSendDecision{Allow: true, Payload: []byte("changed")}, nil
	}), nil)
	submitter := &beforeSendBatchSubmitter{}
	app := New(Options{Submitter: submitter, BeforeSendWebhook: hook})
	items := []SendBatchItem{}
	for _, payload := range []string{"ok", "reject", "last"} {
		items = append(items, SendBatchItem{Command: SendCommand{FromUID: "sender", ChannelID: "group", ChannelType: 2, Payload: []byte(payload), SenderNodeID: 1, SenderSessionID: 1}})
	}
	results := app.SendBatch(items)
	require.Len(t, results, 3)
	require.Equal(t, 3, calls)
	require.Equal(t, ReasonSuccess, results[0].Result.Reason)
	require.Equal(t, Reason(200), results[1].Result.Reason)
	require.Equal(t, ReasonSuccess, results[2].Result.Reason)
	for _, batch := range submitter.batchItems {
		for _, item := range batch {
			require.Equal(t, "changed", string(item.Command.Payload))
		}
	}
}

// beforeSendBatchSubmitter accepts each actual admission wave independently.
type beforeSendBatchSubmitter struct{ batchItems [][]SendBatchItem }

func (s *beforeSendBatchSubmitter) Send(context.Context, SendCommand) (SendResult, error) {
	return SendResult{MessageID: 42}, nil
}
func (s *beforeSendBatchSubmitter) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	s.batchItems = append(s.batchItems, items)
	results := make([]SendBatchItemResult, len(items))
	for i := range results {
		results[i].Result = SendResult{MessageID: uint64(i + 42)}
	}
	return results
}

func TestBeforeSendWebhookSourceChannelIdentityAndOriginalSubmission(t *testing.T) {
	scoped, err := channelid.RequestSubscriberChannelFor([]string{"u2", "u1"})
	require.NoError(t, err)
	for _, tt := range []struct {
		name        string
		cmd         SendCommand
		source      string
		channelType uint8
		suffix      string
	}{
		{"person", SendCommand{ChannelID: "u2", ChannelType: 1, NormalizePersonChannel: true}, channelid.EncodePersonChannel("u1", "u2"), 1, ""},
		{"command", SendCommand{ChannelID: "g1____cmd", ChannelType: 2, SyncOnce: true}, "g1", 2, ""},
		{"subscribers", SendCommand{RequestScoped: true, MessageScopedUIDs: []string{"u2", "u1"}, SyncOnce: true}, scoped.SourceChannelID, scoped.ChannelType, ""},
		{"custom command", SendCommand{ChannelID: "g1____custom", ChannelType: 2, SyncOnce: true}, "g1", 2, "____custom"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.cmd
			cmd.FromUID = "u1"
			cmd.Payload = []byte("original")
			hook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(_ context.Context, req BeforeSendRequest) (BeforeSendDecision, error) {
				require.Equal(t, tt.source, req.ChannelID)
				require.Equal(t, tt.channelType, req.ChannelType)
				return BeforeSendDecision{Allow: true}, nil
			}), nil)
			submitter := &recordingSubmitter{}
			app := New(Options{BeforeSendWebhook: hook, Submitter: submitter, CommandChannelSuffix: tt.suffix})
			_, err := app.Send(context.Background(), cmd)
			require.NoError(t, err)
			if cmd.RequestScoped {
				require.Empty(t, submitter.sendCommand.ChannelID)
				require.Equal(t, cmd.MessageScopedUIDs, submitter.sendCommand.MessageScopedUIDs)
			}
			if cmd.SyncOnce && !cmd.RequestScoped {
				require.Equal(t, cmd.ChannelID, submitter.sendCommand.ChannelID)
			}
		})
	}
}
