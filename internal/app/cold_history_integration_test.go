//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

// TestColdHistoryHTTPClusterRestart checks acknowledged messages through every
// entry node after restarting a single-node cluster or three-node cluster.
func TestColdHistoryHTTPClusterRestart(t *testing.T) {
	for _, count := range []int{1, 3} {
		name := "single-node cluster"
		if count == 3 {
			name = "three-node cluster"
		}
		t.Run(name, func(t *testing.T) {
			voters := make([]cluster.ControlVoter, count)
			for i := range voters {
				voters[i] = cluster.ControlVoter{NodeID: uint64(i + 1), Addr: freeSendackSmokeTCPAddr(t)}
			}
			configs := make([]Config, count)
			for i, voter := range voters {
				cfg := singleNodeClusterAppConfig(t)
				cfg.NodeID, cfg.Cluster.NodeID, cfg.Cluster.ListenAddr = voter.NodeID, voter.NodeID, voter.Addr
				cfg.Cluster.Control.Voters = voters
				cfg.Cluster.Slots.HashSlotCount = 256
				cfg.Cluster.Slots.ReplicaCount = uint16(count)
				cfg.Cluster.Channel.ReplicaCount = uint16(count)
				cfg.API.ListenAddr = "127.0.0.1:0"
				configs[i] = cfg
			}
			var apps []*App
			stop := func() {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				done := make(chan error, len(apps))
				for _, a := range apps {
					go func() { done <- a.Stop(ctx) }()
				}
				for range apps {
					require.NoError(t, <-done)
				}
				apps = nil
			}
			t.Cleanup(stop)
			start := func() {
				t.Helper()
				nodes := make([]*cluster.Node, 0, count)
				for _, cfg := range configs {
					a, err := New(cfg, WithLogger(wklog.NewNop()))
					require.NoError(t, err)
					apps = append(apps, a)
					nodes = append(nodes, a.cluster.(*cluster.Node))
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				done := make(chan error, len(apps))
				for _, a := range apps {
					go func() { done <- a.Start(ctx) }()
				}
				for range apps {
					require.NoError(t, <-done)
				}
				waitAppClusterSnapshotsConverge(t, nodes)
			}
			start()
			type sentMessage struct {
				MessageID   uint64 `json:"message_id"`
				MessageSeq  uint64 `json:"message_seq"`
				ClientMsgNo string `json:"client_msg_no"`
				Payload     string `json:"payload"`
			}
			want := map[uint64]sentMessage{}
			// Every entry node sends to the same person Channel, guaranteeing
			// forwarded SEND and routed history reads in the three-node cluster.
			for i, a := range apps {
				for index := range 2 {
					client := fmt.Sprintf("cold-history-%d-%d", i, index)
					raw := postAppJSON(t, a.api.(*accessapi.Server).Handler(), "/message/send",
						fmt.Sprintf(`{"from_uid":"cold-history-sender","channel_id":"cold-history-receiver","channel_type":1,"client_msg_no":%q,"payload":"aGVsbG8="}`, client), http.StatusOK)
					var sent sentMessage
					require.NoError(t, json.Unmarshal(raw, &sent))
					require.NotZero(t, sent.MessageID)
					require.NotZero(t, sent.MessageSeq)
					sent.ClientMsgNo, sent.Payload = client, "aGVsbG8="
					want[sent.MessageID] = sent
				}
			}
			require.Len(t, want, count*2, "every acknowledged message must have a unique ID")
			verify := func() {
				t.Helper()
				for _, a := range apps {
					var page struct {
						Messages []sentMessage `json:"messages"`
					}
					// Person-directory projection is asynchronous; wait for its
					// existing membership visibility before asserting history.
					deadline := time.Now().Add(15 * time.Second)
					var lastStatus int
					var lastBody string
					for time.Now().Before(deadline) {
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						req := httptest.NewRequest(http.MethodPost, "/channel/messagesync", strings.NewReader(
							`{"login_uid":"cold-history-receiver","channel_id":"cold-history-sender","channel_type":1,"limit":100}`)).WithContext(ctx)
						req.Header.Set("Content-Type", "application/json")
						rec := httptest.NewRecorder()
						a.api.(*accessapi.Server).Handler().ServeHTTP(rec, req)
						cancel()
						lastStatus, lastBody = rec.Code, rec.Body.String()
						if rec.Code == http.StatusOK && json.Unmarshal(rec.Body.Bytes(), &page) == nil && len(page.Messages) == len(want) {
							break
						}
						time.Sleep(25 * time.Millisecond)
					}
					require.Len(t, page.Messages, len(want), "node %d: last response %d %s", a.cfg.NodeID, lastStatus, lastBody)

					seen := make(map[uint64]struct{}, len(page.Messages))
					for _, got := range page.Messages {
						require.NotContains(t, seen, got.MessageID)
						seen[got.MessageID] = struct{}{}
						require.Equal(t, want[got.MessageID], got)
					}
				}
			}
			verify()
			stop()
			start()
			verify()
		})
	}
}
