package webhook

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDurableOutboxRecoversAfterProcessRestartAndDeduplicatesSuccess(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	opts := durableOutboxTestOptions(dir, func() time.Time { return now })
	box, err := OpenDurableOutbox(opts)
	if err != nil {
		t.Fatalf("OpenDurableOutbox() error = %v", err)
	}
	entry := OutboxEntry{ID: "wh_restart", Event: EventMsgNotify, Body: []byte(`[{"message_id":1}]`), Items: 1}
	inserted, err := box.Enqueue(context.Background(), entry)
	if err != nil || !inserted {
		t.Fatalf("Enqueue() inserted=%v error=%v", inserted, err)
	}
	if err := box.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	box, err = OpenDurableOutbox(opts)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer box.Close()
	claimed, err := box.ClaimDue(now)
	if err != nil || len(claimed) != 1 || string(claimed[0].Body) != string(entry.Body) {
		t.Fatalf("ClaimDue() = %#v, error=%v", claimed, err)
	}
	if err := box.MarkDelivered(claimed[0], now.Add(time.Second)); err != nil {
		t.Fatalf("MarkDelivered() error = %v", err)
	}
	inserted, err = box.Enqueue(context.Background(), entry)
	if err != nil || inserted {
		t.Fatalf("delivered dedupe inserted=%v error=%v", inserted, err)
	}
	stats, err := box.Stats(context.Background())
	if err != nil || stats.Backlog != 0 || stats.DeliveredTombstones != 1 {
		t.Fatalf("Stats() = %#v, error=%v", stats, err)
	}
}

func TestDurableOutboxRetriesMovesToDeadLetterAndSupportsBoundedReplay(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	box, err := OpenDurableOutbox(durableOutboxTestOptions(
		t.TempDir(), func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenDurableOutbox() error = %v", err)
	}
	defer box.Close()
	entry := OutboxEntry{ID: "wh_dead", Event: EventMsgOffline, Body: []byte(`{"msg":{}}`), Items: 2}
	if inserted, err := box.Enqueue(context.Background(), entry); err != nil || !inserted {
		t.Fatalf("Enqueue() inserted=%v error=%v", inserted, err)
	}
	claimed, _ := box.ClaimDue(now)
	dead, err := box.MarkFailed(claimed[0], 2, now)
	if err != nil || dead {
		t.Fatalf("first MarkFailed() dead=%v error=%v", dead, err)
	}
	if early, _ := box.ClaimDue(now); len(early) != 0 {
		t.Fatalf("retry was claimable before backoff: %#v", early)
	}
	now = now.Add(10 * time.Millisecond)
	claimed, _ = box.ClaimDue(now)
	dead, err = box.MarkFailed(claimed[0], 2, now)
	if err != nil || !dead {
		t.Fatalf("second MarkFailed() dead=%v error=%v", dead, err)
	}
	stats, _ := box.Stats(context.Background())
	if stats.Backlog != 0 || stats.DeadLetters != 1 || stats.RetryAttempts != 2 ||
		len(stats.DeadLetterEntries) != 1 || stats.DeadLetterEntries[0].ID != entry.ID ||
		stats.DeadLetterEntries[0].LastErrorCode != "WEBHOOK_SEND_FAILED" {
		t.Fatalf("dead-letter stats = %#v", stats)
	}
	if _, err := box.ReplayDead(context.Background(), []string{entry.ID, entry.ID}); err == nil {
		t.Fatal("ReplayDead() duplicate ids error = nil")
	}
	replayed, err := box.ReplayDead(context.Background(), []string{entry.ID})
	if err != nil || replayed != 1 {
		t.Fatalf("ReplayDead() replayed=%d error=%v", replayed, err)
	}
	stats, _ = box.Stats(context.Background())
	if stats.Backlog != 1 || stats.DeadLetters != 0 {
		t.Fatalf("replayed stats = %#v", stats)
	}
}

func TestDurableOutboxCapacityBackpressuresUntilContextCancellation(t *testing.T) {
	opts := durableOutboxTestOptions(t.TempDir(), time.Now)
	opts.MaxEntries = 1
	box, err := OpenDurableOutbox(opts)
	if err != nil {
		t.Fatalf("OpenDurableOutbox() error = %v", err)
	}
	defer box.Close()
	if _, err := box.Enqueue(context.Background(), OutboxEntry{
		ID: "wh_one", Event: EventMsgNotify, Body: []byte(`[]`), Items: 1,
	}); err != nil {
		t.Fatalf("first Enqueue() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	_, err = box.Enqueue(ctx, OutboxEntry{
		ID: "wh_two", Event: EventMsgNotify, Body: []byte(`[]`), Items: 1,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Enqueue() error = %v, want deadline", err)
	}
	stats, _ := box.Stats(context.Background())
	if stats.Backlog != 1 {
		t.Fatalf("Stats() = %#v, want one retained item", stats)
	}
}

func TestDurableOutboxDeliveredMarkersDoNotConsumeLiveEntryCapacity(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	opts := durableOutboxTestOptions(t.TempDir(), func() time.Time { return now })
	opts.MaxEntries = 1
	box, err := OpenDurableOutbox(opts)
	if err != nil {
		t.Fatalf("OpenDurableOutbox() error = %v", err)
	}
	defer box.Close()
	first := OutboxEntry{ID: "wh_first", Event: EventMsgNotify, Body: []byte(`[]`), Items: 1}
	if inserted, err := box.Enqueue(context.Background(), first); err != nil || !inserted {
		t.Fatalf("first Enqueue() inserted=%v error=%v", inserted, err)
	}
	claimed, err := box.ClaimDue(now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimDue()=%#v error=%v", claimed, err)
	}
	if err := box.MarkDelivered(claimed[0], now); err != nil {
		t.Fatalf("MarkDelivered() error=%v", err)
	}
	if inserted, err := box.Enqueue(context.Background(), OutboxEntry{
		ID: "wh_second", Event: EventMsgNotify, Body: []byte(`[]`), Items: 1,
	}); err != nil || !inserted {
		t.Fatalf("second Enqueue() inserted=%v error=%v", inserted, err)
	}
	stats, err := box.Stats(context.Background())
	if err != nil || stats.Backlog != 1 || stats.DeliveredTombstones != 1 {
		t.Fatalf("Stats()=%#v error=%v", stats, err)
	}
}

func TestStableEventIDIsRecipientOrderIndependentAndTargetScoped(t *testing.T) {
	message := Message{MessageID: 9, MessageSeq: 7, ChannelID: "c", ChannelType: 1}
	left := StableEventID(EventMsgOffline, message, []string{"u2", "u1"})
	right := StableEventID(EventMsgOffline, message, []string{"u1", "u2"})
	if left != right {
		t.Fatalf("stable ids differ: %q / %q", left, right)
	}
	if left == StableEventID(EventMsgOffline, message, []string{"u1"}) {
		t.Fatalf("different recipient chunk reused id %q", left)
	}
}

func durableOutboxTestOptions(dir string, now func() time.Time) OutboxOptions {
	return OutboxOptions{
		Dir: dir, MaxEntries: 10, MaxBytes: 1 << 20, DispatchBatchSize: 10,
		RetryBaseDelay: 5 * time.Millisecond, RetryMaxDelay: time.Second,
		DeliveredRetention: time.Hour, Now: now,
	}
}
