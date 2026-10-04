package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// Each original WAL record keeps its identity even when HTTP delivery is batched.
func TestNotifyBatchKeepsDurableIdentitiesAndOutcomes(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed"}[fail], func(t *testing.T) {
			box, err := OpenDurableOutbox(testOutboxOptions(t))
			require.NoError(t, err)
			defer box.Close()
			entries := []OutboxEntry{{ID: "first", Event: EventMsgNotify, Body: []byte(`[{"message_id":1}]`), Items: 1}, {ID: "second", Event: EventMsgNotify, Body: []byte(`[{"message_id":2}]`), Items: 1}}
			for _, entry := range entries {
				_, err = box.Enqueue(context.Background(), entry)
				require.NoError(t, err)
			}
			claimed, err := box.ClaimDue(time.Now())
			require.NoError(t, err)
			require.Len(t, claimed, 2)
			var sent SendRequest
			sender := mergeBatchSender(func(_ context.Context, r SendRequest) error {
				sent = r
				if fail {
					return errors.New("offline")
				}
				return nil
			})
			rt := &Runtime{opts: RuntimeOptions{RequestTimeout: time.Second, RetryMaxAttempts: 1}, sender: sender}
			rt.dispatchOutboxNotifyBatch(context.Background(), box, claimed)
			var body []struct {
				EventID   string `json:"event_id"`
				MessageID uint64 `json:"message_id"`
			}
			require.NoError(t, json.Unmarshal(sent.Body, &body))
			require.Len(t, body, 2)
			require.NotEqual(t, body[0].EventID, body[1].EventID)
			require.NotEmpty(t, sent.ID)
			stats, err := box.Stats(context.Background())
			require.NoError(t, err)
			require.Zero(t, stats.Backlog)
			if fail {
				require.Equal(t, 2, stats.DeadLetters)
			} else {
				require.Equal(t, 2, stats.DeliveredTombstones)
			}
		})
	}
}

type mergeBatchSender func(context.Context, SendRequest) error

func (f mergeBatchSender) Send(ctx context.Context, r SendRequest) error { return f(ctx, r) }
