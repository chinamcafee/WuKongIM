//go:build integration

package migrationv2_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	product "github.com/WuKongIM/WuKongIM/internal/app"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

// TestStreamOnlyMigrationHTTP checks actual installed output through product
// HTTP listeners, including empty history, membership fences, and native SEND.
func TestStreamOnlyMigrationHTTP(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d_node_cluster", count), func(t *testing.T) { testMigratedMembershipHTTP(t, count, false) })
	}
}

// A recovered read conversation must expose old history and count only new sends,
// including after two complete cluster restarts and through every HTTP entry node.
func TestMissingConversationRecoveryHTTP(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d_node_cluster", count), func(t *testing.T) { testMigratedMembershipHTTP(t, count, true) })
	}
}

func testMigratedMembershipHTTP(t *testing.T, count int, missing bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	source := ""
	if missing {
		source = missingConversationFixture(t)
	} else {
		source = streamExclusionFixture(t, true)
	}
	p := diagnosticPlan(t, source)
	if missing {
		p.Metadata = conversationPolicy()
		captureWorkspace, e := transfer.OpenSpool(filepath.Join(t.TempDir(), "capture"), p.Digest(), 128<<20)
		require.NoError(t, e)
		capture, e := migration.CaptureSources(ctx, p.Sources, migrationv2.Reader{}, captureWorkspace, nil)
		require.NoError(t, e)
		require.NoError(t, captureWorkspace.Close())
		p.Metadata.MissingConversations = []migration.MissingConversationRecovery{missingPin(capture.Digest, "migrationalice"), missingPin(capture.Digest, "migrationbob")}
	}
	p.Messages = &migration.MessagePolicy{ExcludeCMD: true, ExcludeStreams: true, CompactSequences: true}
	p.Target.Nodes = nil
	p.Target.Replicas = uint16(count)
	p.Target.ChannelReplicas = uint16(count)
	voters := make([]cluster.ControlVoter, count)
	freeAddr := func() string {
		t.Helper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := l.Addr().String()
		require.NoError(t, l.Close())
		return addr
	}
	for i := 0; i < count; i++ {
		id := uint64(101 + i)
		addr := freeAddr()
		voters[i] = cluster.ControlVoter{NodeID: id, Addr: addr}
		p.Target.Nodes = append(p.Target.Nodes, migration.TargetNode{NodeID: id, Addr: addr, DataDir: filepath.Join(t.TempDir(), "cluster")})
	}
	w, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
	require.NoError(t, err)
	defer w.Close()
	r := migrationv2.Reader{}
	prepared, err := migration.Prepare(ctx, p, w, r, r, nil)
	require.NoError(t, err)
	if missing {
		require.EqualValues(t, 3, prepared.Conversion.Messages)
	} else {
		require.Zero(t, prepared.Conversion.Messages)
	}
	require.NoError(t, migrationv3.Install(ctx, p.Target, prepared.Conversion, w))
	_, err = migration.VerifyTargets(ctx, p.Target, prepared.Selection, w, r, migrationv3.Inspector{})
	require.NoError(t, err)
	configs := make([]product.Config, count)
	urls := make([]string, count)
	for i, n := range p.Target.Nodes {
		plugin := product.PluginConfig{Enable: false}
		plugin.SetEnableExplicit(true)
		plugin.SetExplicitFlags(true)
		addr := freeAddr()
		urls[i] = "http://" + addr
		configs[i] = product.Config{NodeID: n.NodeID, DataDir: t.TempDir(), Log: product.LogConfig{Dir: t.TempDir()}, Plugin: plugin, API: product.APIConfig{ListenAddr: addr}, Cluster: cluster.Config{NodeID: n.NodeID, ListenAddr: n.Addr, DataDir: n.DataDir, Control: cluster.ControlConfig{ClusterID: p.Target.ClusterID, Voters: voters}, Slots: cluster.SlotConfig{InitialSlotCount: p.Target.SlotCount, HashSlotCount: 256, ReplicaCount: uint16(count)}, Channel: cluster.ChannelConfig{ReplicaCount: uint16(count)}}}
	}
	var apps []*product.App
	stop := func() {
		t.Helper()
		stopCtx, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		errs := make(chan error, len(apps))
		for _, a := range apps {
			go func() { errs <- a.Stop(stopCtx) }()
		}
		for range apps {
			require.NoError(t, <-errs)
		}
		apps = nil
	}
	t.Cleanup(stop)
	start := func() {
		t.Helper()
		for _, cfg := range configs {
			a, err := product.New(cfg, product.WithLogger(wklog.NewNop()))
			require.NoError(t, err)
			apps = append(apps, a)
		}
		errs := make(chan error, count)
		for _, a := range apps {
			go func() { errs <- a.Start(ctx) }()
		}
		for range apps {
			require.NoError(t, <-errs)
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	post := func(url, path, body string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+path, strings.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, raw, err
	}
	mustPost := func(url, path, body string, want int) []byte {
		t.Helper()
		status, raw, err := post(url, path, body)
		require.NoError(t, err)
		require.Equal(t, want, status, "%s: %s", path, raw)
		return raw
	}
	type message struct {
		ID      uint64 `json:"message_id"`
		Seq     uint64 `json:"message_seq"`
		Client  string `json:"client_msg_no"`
		Payload string `json:"payload"`
	}
	type page struct {
		Messages []message `json:"messages"`
		More     int       `json:"more"`
		Error    string    `json:"error"`
	}
	want := []message{}
	if missing {
		require.NoError(t, migration.WalkTargetMessages(ctx, w, migration.ChannelIdentity{ID: "migrationgroup", Type: 2}, func(m channelcompat.Message) error {
			want = append(want, message{ID: m.MessageID, Seq: m.MessageSeq, Client: m.ClientMsgNo, Payload: base64.StdEncoding.EncodeToString(m.Payload)})
			return nil
		}))
	}
	initialTail := len(want)
	syncBody := `{"login_uid":"migrationbob","channel_id":"migrationgroup","channel_type":2,"limit":10}`
	batchBody := `{"login_uid":"migrationbob","items":[{"channel_id":"migrationgroup","channel_type":2,"limit":10}]}`
	check := func() {
		t.Helper()
		for _, url := range urls {
			var raw []byte
			var status int
			var callErr error
			var got page
			require.Eventually(t, func() bool {
				status, raw, callErr = post(url, "/channel/messagesync", syncBody)
				return callErr == nil && status == http.StatusOK && json.Unmarshal(raw, &got) == nil && len(got.Messages) == len(want)
			}, 20*time.Second, 50*time.Millisecond)
			require.NotNil(t, got.Messages)
			require.Equal(t, want, got.Messages)
			require.Zero(t, got.More)
			require.Empty(t, got.Error)
			raw = mustPost(url, "/channel/messagesyncbatch", batchBody, http.StatusOK)
			var batch struct {
				Items []page `json:"items"`
			}
			require.NoError(t, json.Unmarshal(raw, &batch))
			require.Len(t, batch.Items, 1)
			require.Equal(t, want, batch.Items[0].Messages)
			require.Empty(t, batch.Items[0].Error)
			require.Zero(t, batch.Items[0].More)
			raw = mustPost(url, "/conversation/sync", `{"uid":"migrationbob","msg_count":10}`, http.StatusOK)
			var conversations []struct {
				ChannelID string    `json:"channel_id"`
				Unread    int       `json:"unread"`
				Recents   []message `json:"recents"`
			}
			require.NoError(t, json.Unmarshal(raw, &conversations))
			if len(want) == 0 {
				require.Empty(t, conversations)
			} else {
				require.Len(t, conversations, 1)
				require.Equal(t, "migrationgroup", conversations[0].ChannelID)
				require.Equal(t, len(want)-initialTail, conversations[0].Unread)
				require.Len(t, conversations[0].Recents, len(want))
				for i, m := range conversations[0].Recents {
					require.Equal(t, want[len(want)-1-i], m)
				}
			}
			mustPost(url, "/channel/messagesync", `{"login_uid":"not-a-member","channel_id":"migrationgroup","channel_type":2,"limit":10}`, http.StatusBadRequest)
		}
	}
	for run := 0; run < 3; run++ {
		start()
		check()
		if run == 0 {
			for _, url := range urls {
				mustPost(url, "/conversations/clearUnread", `{"uid":"migrationbob","channel_id":"migrationgroup","channel_type":2}`, http.StatusOK)
				mustPost(url, "/conversations/setUnread", `{"uid":"migrationbob","channel_id":"migrationgroup","channel_type":2,"unread":0}`, http.StatusOK)
			}
		}
		if run < 2 {
			clientNo := fmt.Sprintf("normal-after-exclusion-%d", run)
			raw := mustPost(urls[run%count], "/message/send", fmt.Sprintf(`{"from_uid":"migrationalice","channel_id":"migrationgroup","channel_type":2,"client_msg_no":%q,"payload":"aGVsbG8="}`, clientNo), http.StatusOK)
			var ack message
			require.NoError(t, json.Unmarshal(raw, &ack))
			require.NotZero(t, ack.ID)
			require.EqualValues(t, initialTail+run+1, ack.Seq)
			for _, prior := range want {
				require.NotEqual(t, prior.ID, ack.ID)
			}
			ack.Client = clientNo
			ack.Payload = "aGVsbG8="
			want = append(want, ack)
			check()
		}
		stop()
	}
}
