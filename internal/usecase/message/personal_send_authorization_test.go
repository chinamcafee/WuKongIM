package message

import (
	"context"
	"errors"
	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"testing"
)

type accountAuthorizer struct {
	calls           []string
	unavailable     bool
	systemRecipient bool
}

func (a *accountAuthorizer) Authorize(_ context.Context, sender, recipient string, system bool) (bool, error) {
	a.calls = append(a.calls, sender+":"+recipient)
	a.systemRecipient = system
	if a.unavailable {
		return false, errors.New("fixture authority unavailable")
	}
	return sender != "inactive" && recipient != "inactive", nil
}

func TestPersonalAdmissionCannotBeBypassedByMetadataOrInternalFlags(t *testing.T) {
	for _, command := range []SendCommand{
		{FromUID: "active", ChannelID: "inactive", ChannelType: 1, NormalizePersonChannel: true},
		{FromUID: "active", ChannelID: runtimechannelid.ToCommandChannel(runtimechannelid.EncodePersonChannel("active", "inactive")), ChannelType: 1, SkipPluginHooks: true},
		{FromUID: "active", ChannelID: "inactive", ChannelType: 1, NormalizePersonChannel: true, DeviceID: "____device"},
		{FromUID: "active", ChannelID: "inactive", ChannelType: 1, NormalizePersonChannel: true, RequestScoped: true},
		{FromUID: "active", ChannelID: "", ChannelType: 1, RequestScoped: true, MessageScopedUIDs: []string{"inactive"}},
		{FromUID: "active", ChannelType: 0, RequestScoped: true, MessageScopedUIDs: []string{"inactive"}},
		{FromUID: "inactive", ChannelType: 0, RequestScoped: true, MessageScopedUIDs: []string{"active"}},
		{FromUID: "active", ChannelType: 0, MessageScopedUIDs: []string{"inactive"}},
		{FromUID: "active", ChannelID: "inactive", ChannelType: 1, MessageScopedUIDs: []string{"active"}, NormalizePersonChannel: true},
	} {
		guard := &accountAuthorizer{}
		submitter := &recordingSubmitter{}
		permissions := newFakePermissionStore()
		allow := channelmembers.AllowlistChannelID(channelmembers.ChannelKey{ChannelID: "inactive", ChannelType: 1})
		permissions.members[permissionKey(allow, 1)] = map[string]bool{"active": true}
		app := New(Options{Submitter: submitter, PermissionStore: permissions, PersonWhitelistEnabled: true, SystemDeviceID: "____device", PersonalSendAuthorizer: guard})
		result, err := app.Send(context.Background(), command)
		if err != nil || result.Reason != ReasonNotAllowSend || submitter.sendCommand.FromUID != "" {
			t.Fatalf("command %v admitted: %v %v", command, result, err)
		}
		if len(guard.calls) == 0 {
			t.Fatal("authoritative account admission was skipped")
		}
	}
}

func TestPersonalAdmissionBatchKeepsPerItemDenialAndNoPositiveCache(t *testing.T) {
	guard := &accountAuthorizer{}
	submitter := &recordingSubmitter{}
	app := New(Options{Submitter: submitter, PersonalSendAuthorizer: guard})
	results := app.SendBatch([]SendBatchItem{
		{Command: SendCommand{FromUID: "active", ChannelID: "inactive", ChannelType: 1}},
		{Command: SendCommand{FromUID: "active", ChannelID: "receiver", ChannelType: 1}},
		{Command: SendCommand{FromUID: "inactive", ChannelID: "receiver", ChannelType: 1}},
	})
	if results[0].Result.Reason != ReasonNotAllowSend || results[2].Result.Reason != ReasonNotAllowSend {
		t.Fatal("inactive item did not reject")
	}
	if len(submitter.batchItems) != 1 || len(submitter.batchItems[0]) != 1 || len(guard.calls) != 3 {
		t.Fatal("batch was not filtered before append")
	}
	guard.unavailable = true
	result, err := app.Send(context.Background(), SendCommand{FromUID: "active", ChannelID: "receiver", ChannelType: 1})
	if err == nil || result.Reason != ReasonSystemError || submitter.sendCommand.FromUID != "" {
		t.Fatal("earlier allowed decision was reused")
	}
}

func TestPersonalAdmissionRunsAfterPluginMutationAndPreservesTrustedSystemNotifications(t *testing.T) {
	guard := &accountAuthorizer{}
	submitter := &recordingSubmitter{}
	hook := &recordingSendHook{mutate: func(c SendCommand) (SendCommand, Reason, error) {
		c.ChannelID = "inactive"
		return c, ReasonSuccess, nil
	}}
	app := New(Options{Submitter: submitter, PersonalSendAuthorizer: guard, SendHook: hook})
	result, err := app.Send(context.Background(), SendCommand{FromUID: "active", ChannelID: "receiver", ChannelType: 1})
	if err != nil || result.Reason != ReasonNotAllowSend {
		t.Fatal("plugin-mutated receiver bypassed admission")
	}
	app = New(Options{Submitter: submitter, PersonalSendAuthorizer: guard, SystemUIDs: fakeSystemUIDChecker{"official": true}})
	_, err = app.Send(context.Background(), SendCommand{FromUID: "official", ChannelID: "inactive", ChannelType: 1})
	if err != nil || len(guard.calls) != 1 || submitter.sendCommand.FromUID != "official" {
		t.Fatal("trusted system notification path changed")
	}
	_, err = app.Send(context.Background(), SendCommand{FromUID: "active", ChannelID: "official", ChannelType: 1})
	if err != nil || !guard.systemRecipient {
		t.Fatal("normal sender to system still requires account admission")
	}
}
