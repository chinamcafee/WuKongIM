package message

import (
	"context"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// authorizePersonalSend cannot be skipped with plugin, system-device or request-scoped flags.
// Only an authoritative system sender bypasses the business account admission.
func (a *App) authorizePersonalSend(ctx context.Context, cmd SendCommand) (Reason, error) {
	if a == nil || a.personalSendAuthorizer == nil {
		return ReasonSuccess, nil
	}
	// HTTP subscriber sends use channel type zero but still target individual UIDs.
	if cmd.ChannelType != channelTypePerson && !cmd.RequestScoped && !(len(cmd.MessageScopedUIDs) > 0 && cmd.ChannelID == "") {
		return ReasonSuccess, nil
	}
	if a.systemUIDs != nil && a.systemUIDs.IsSystemUID(cmd.FromUID) {
		return ReasonSuccess, nil
	}
	recipients := append([]string(nil), cmd.MessageScopedUIDs...)
	if len(recipients) > 1024 {
		return ReasonInvalidRequest, nil
	}
	if cmd.ChannelID != "" {
		source, _ := runtimechannelid.FromCommandChannel(cmd.ChannelID)
		channel, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, source)
		if err != nil {
			return ReasonInvalidRequest, err
		}
		left, right, err := runtimechannelid.DecodePersonChannel(channel)
		if err != nil {
			return ReasonInvalidRequest, err
		}
		if cmd.FromUID != left && cmd.FromUID != right {
			return ReasonInvalidRequest, nil
		}
		receiver := right
		if cmd.FromUID == right {
			receiver = left
		}
		recipients = append(recipients, receiver)
	}
	if len(recipients) == 0 {
		return ReasonInvalidRequest, nil
	}
	for _, receiver := range recipients {
		isSystem := a.systemUIDs != nil && a.systemUIDs.IsSystemUID(receiver)
		allowed, err := a.personalSendAuthorizer.Authorize(ctx, cmd.FromUID, receiver, isSystem)
		if err != nil {
			return ReasonSystemError, err
		}
		if !allowed {
			return ReasonNotAllowSend, nil
		}
	}
	return ReasonSuccess, nil
}
