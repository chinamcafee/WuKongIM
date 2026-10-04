package channelappend

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	channelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestPersonCMDCanonicalPreparationAndRecipients(t *testing.T) {
	for _, suffix := range []string{"", "__commands"} {
		for _, noPersist := range []bool{false, true} {
			for _, sender := range []string{"____system", "uu1"} {
				peer := "uu1"
				if sender == peer {
					peer = "____system"
				}
				codec := channelid.CommandCodec{Suffix: suffix}
				canonical := channelid.EncodePersonChannel(sender, peer)
				for _, input := range []string{peer, canonical, codec.ToCommandChannel(canonical)} {
					t.Run(fmt.Sprintf("suffix=%s/transient=%v/sender=%s/input=%s", suffix, noPersist, sender, input), func(t *testing.T) {
						cmd := SendCommand{FromUID: sender, ChannelID: input, ChannelType: 1, Payload: []byte(`{"type":99,"cmd":"clearUnread","param":{"channelID":"gfh","channelType":1}}`), NoPersist: noPersist, SyncOnce: true, NormalizePersonChannel: true, MessageID: 123}
						target, result, done := preRouteChannel(cmd, codec)
						if done {
							t.Fatalf("route rejected: %+v", result)
						}
						prepared, done := prepareSend(context.Background(), cmd, preparePorts{commandChannels: codec}, false)
						if prepared.err != nil || done {
							t.Fatalf("valid person CMD rejected: %v (done=%v)", prepared.err, done)
						}
						if prepared.item.Command.ChannelID != target.ID || target.ID != codec.ToCommandChannel(canonical) {
							t.Fatalf("route=%q prepared=%q", target.ID, prepared.item.Command.ChannelID)
						}
						enqueuer := &recordingRecipientEnqueuerForRecipientTest{}
						mode := onlinedelivery.ModeDurable
						if noPersist {
							mode = onlinedelivery.ModeTransient
						}
						event := committedEnvelopeForRealtime(prepared.item)
						_, err := dispatchRecipientsForTarget(context.Background(), mode, AuthorityTarget{ChannelID: target}, event, subscriberCache{}, commitPorts{commandChannels: codec, recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{nodeID: 7}, deliveryEnqueuer: enqueuer, recipientBatchSize: 16})
						if err != nil {
							t.Fatal(err)
						}
						left, right, _ := channelid.DecodePersonChannel(canonical)
						if got := enqueuer.allUIDs(); !reflect.DeepEqual(got, []string{left, right}) {
							t.Fatalf("recipients=%v want real participants %v", got, []string{left, right})
						}
					})
				}
			}
		}
	}
}

func TestRequestScopedCMDUsesConfiguredSuffix(t *testing.T) {
	for _, suffix := range []string{"", "__commands"} {
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			codec := channelid.CommandCodec{Suffix: suffix}
			cmd := SendCommand{FromUID: "____system", Payload: []byte("cmd"), NoPersist: true, SyncOnce: true, RequestScoped: true, MessageScopedUIDs: []string{"uu1"}, MessageID: 123}
			target, result, done := preRouteChannel(cmd, codec)
			if done {
				t.Fatalf("route rejected: %+v", result)
			}
			p, done := prepareSend(context.Background(), cmd, preparePorts{commandChannels: codec}, false)
			if p.err != nil || done {
				t.Fatalf("prepare: done=%v err=%v", done, p.err)
			}
			if target.ID != p.item.Command.ChannelID || !codec.IsCommandChannel(target.ID) {
				t.Fatalf("route=%q prepared=%q", target.ID, p.item.Command.ChannelID)
			}
			if !reflect.DeepEqual(p.item.Command.MessageScopedUIDs, []string{"uu1"}) {
				t.Fatalf("recipients=%v", p.item.Command.MessageScopedUIDs)
			}
		})
	}
}
