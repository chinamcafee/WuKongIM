//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestEmptyPersonConversationAPIsBeforeFirstMessage(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	cfg.API.ListenAddr = "127.0.0.1:0"
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	node := app.cluster.(*cluster.Node)
	waitSingleNodeClusterRouteLeader(t, node, "empty-alice", cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	handler := app.api.(*accessapi.Server).Handler()
	checkEmpty := func(t *testing.T) {
		for _, test := range []struct {
			path, body string
		}{
			{"/channel/messagesync", `{"login_uid":"empty-alice","channel_id":"empty-bob","channel_type":1,"limit":30}`},
			{"/channel/messagesyncbatch", `{"login_uid":"empty-alice","items":[{"channel_id":"empty-bob","channel_type":1}]}`},
			{"/conversations/setUnread", `{"uid":"empty-alice","channel_id":"empty-bob","channel_type":1,"unread":0}`},
			{"/conversations/clearUnread", `{"uid":"empty-alice","channel_id":"empty-bob","channel_type":1}`},
		} {
			t.Run(test.path, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("empty conversation: HTTP %d %s, want HTTP 200", rec.Code, rec.Body.String())
				}
				switch test.path {
				case "/channel/messagesync":
					var page struct {
						Messages []json.RawMessage `json:"messages"`
						More     int               `json:"more"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.Messages == nil || len(page.Messages) != 0 || page.More != 0 {
						t.Fatalf("empty page = %s, err=%v", rec.Body.String(), err)
					}
				case "/channel/messagesyncbatch":
					var batch struct {
						Items []struct {
							ChannelID string            `json:"channel_id"`
							Messages  []json.RawMessage `json:"messages"`
							More      int               `json:"more"`
							Error     string            `json:"error"`
						} `json:"items"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil || len(batch.Items) != 1 {
						t.Fatalf("empty batch = %s, err=%v", rec.Body.String(), err)
					}
					item := batch.Items[0]
					if item.ChannelID != "empty-bob" || item.Messages == nil || len(item.Messages) != 0 || item.More != 0 || item.Error != "" {
						t.Fatalf("empty batch item = %s", rec.Body.String())
					}
				default:
					var mutation struct {
						Status int `json:"status"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &mutation); err != nil || mutation.Status != http.StatusOK {
						t.Fatalf("mutation = %s, err=%v", rec.Body.String(), err)
					}
				}
			})
		}
	}
	t.Run("missing membership", checkEmpty)
	channelID := runtimechannelid.EncodePersonChannel("empty-alice", "empty-bob")
	if _, found, err := node.GetUserChannelMembership(context.Background(), "empty-alice", channelID, 1); err != nil || found {
		t.Fatalf("opening empty chat created membership: found=%v err=%v", found, err)
	}
	if err := node.UpsertUserChannelMemberships(context.Background(), channelID, 1, []string{"empty-alice"}, 0, 1, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	t.Run("membership without messages", checkEmpty)
}
