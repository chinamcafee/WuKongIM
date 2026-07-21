package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

var (
	ErrOutboxClosed = errors.New("webhook: outbox closed")
	ErrOutboxFull   = errors.New("webhook: outbox capacity exhausted")
)

const (
	outboxActivePrefix    = "a/"
	outboxDeadPrefix      = "d/"
	outboxDeliveredPrefix = "s/"
	outboxSchedulePrefix  = "q/"
)

// OutboxOptions bounds durable webhook storage and retry scheduling.
type OutboxOptions struct {
	Dir string
	// MaxEntries bounds active and dead delivery records; delivered dedupe markers remain byte-bounded.
	MaxEntries         int
	MaxBytes           int64
	DispatchBatchSize  int
	RetryBaseDelay     time.Duration
	RetryMaxDelay      time.Duration
	DeliveredRetention time.Duration
	Now                func() time.Time
}

// OutboxEntry is one stable, durable HTTP webhook delivery unit.
type OutboxEntry struct {
	ID            string `json:"id"`
	Event         string `json:"event"`
	Body          []byte `json:"body"`
	Items         int    `json:"items"`
	Attempt       int    `json:"attempt"`
	CreatedAtNS   int64  `json:"created_at_ns"`
	NextAttemptNS int64  `json:"next_attempt_ns"`
	LastErrorCode string `json:"last_error_code,omitempty"`
}

// OutboxStats is a low-cardinality operational snapshot.
type OutboxStats struct {
	Backlog             int                `json:"backlog"`
	DeadLetters         int                `json:"dead_letters"`
	DeliveredTombstones int                `json:"delivered_tombstones"`
	LogicalBytes        int64              `json:"logical_bytes"`
	OldestAge           time.Duration      `json:"oldest_age"`
	RetryAttempts       int64              `json:"retry_attempts"`
	OldestCreatedAt     time.Time          `json:"oldest_created_at,omitempty"`
	DeadLetterEntries   []OutboxDeadLetter `json:"dead_letter_entries,omitempty"`
}

// OutboxDeadLetter is the bounded, body-free operator view of one dead delivery.
type OutboxDeadLetter struct {
	ID            string    `json:"id"`
	Event         string    `json:"event"`
	Items         int       `json:"items"`
	Attempt       int       `json:"attempt"`
	CreatedAt     time.Time `json:"created_at"`
	LastErrorCode string    `json:"last_error_code"`
}

type deliveredMarker struct {
	ID            string `json:"id"`
	DeliveredAtNS int64  `json:"delivered_at_ns"`
}

// DurableOutbox stores accepted critical webhook work in a sync-WAL Pebble DB.
type DurableOutbox struct {
	db   *pebble.DB
	opts OutboxOptions

	mu           sync.Mutex
	closed       bool
	logicalBytes int64
	entries      int
	inflight     map[string]struct{}
	wake         chan struct{}
}

// OpenDurableOutbox opens and audits the node-local webhook outbox.
func OpenDurableOutbox(opts OutboxOptions) (*DurableOutbox, error) {
	opts.Dir = strings.TrimSpace(opts.Dir)
	if opts.Dir == "" || opts.MaxEntries <= 0 || opts.MaxBytes <= 0 ||
		opts.DispatchBatchSize <= 0 || opts.RetryBaseDelay <= 0 ||
		opts.RetryMaxDelay < opts.RetryBaseDelay || opts.DeliveredRetention <= 0 {
		return nil, fmt.Errorf("webhook: invalid outbox config")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	db, err := pebble.Open(opts.Dir, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("webhook: open outbox: %w", err)
	}
	box := &DurableOutbox{
		db:       db,
		opts:     opts,
		inflight: make(map[string]struct{}),
		wake:     make(chan struct{}, 1),
	}
	if err := box.recountLocked(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if box.entries > opts.MaxEntries || box.logicalBytes > opts.MaxBytes {
		_ = db.Close()
		return nil, fmt.Errorf("webhook: existing outbox exceeds configured capacity")
	}
	return box, nil
}

// StableEventID derives the delivery identity used for HTTP retries and dedupe.
func StableEventID(event string, message Message, recipientUIDs []string) string {
	uids := append([]string(nil), recipientUIDs...)
	sort.Strings(uids)
	canonical := strings.Join([]string{
		event,
		strconv.FormatUint(message.MessageID, 10),
		strconv.FormatUint(message.MessageSeq, 10),
		message.ChannelID,
		strconv.Itoa(int(message.ChannelType)),
		strings.Join(uids, ","),
	}, "|")
	digest := sha256.Sum256([]byte(canonical))
	return "wh_" + hex.EncodeToString(digest[:16])
}

// Enqueue syncs one critical delivery before returning. A delivered/dead/pending
// identity is a successful no-op, so source retries never create duplicates.
func (o *DurableOutbox) Enqueue(ctx context.Context, entry OutboxEntry) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateOutboxEntry(entry); err != nil {
		return false, err
	}
	for {
		inserted, err := o.enqueueOnce(entry)
		if !errors.Is(err, ErrOutboxFull) {
			return inserted, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (o *DurableOutbox) enqueueOnce(entry OutboxEntry) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return false, ErrOutboxClosed
	}
	for _, prefix := range []string{outboxActivePrefix, outboxDeadPrefix, outboxDeliveredPrefix} {
		exists, err := o.hasLocked([]byte(prefix + entry.ID))
		if err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
	}
	now := o.opts.Now().UTC()
	entry.Attempt = 0
	entry.CreatedAtNS = now.UnixNano()
	entry.NextAttemptNS = entry.CreatedAtNS
	entry.LastErrorCode = ""
	value, err := json.Marshal(entry)
	if err != nil {
		return false, fmt.Errorf("webhook: encode outbox entry: %w", err)
	}
	activeKey := []byte(outboxActivePrefix + entry.ID)
	scheduledKey := outboxScheduleKey(entry)
	logicalSize := int64(len(activeKey) + len(value) + len(scheduledKey))
	if o.entries+1 > o.opts.MaxEntries || o.logicalBytes+logicalSize > o.opts.MaxBytes {
		return false, ErrOutboxFull
	}
	batch := o.db.NewBatch()
	defer batch.Close()
	if err := batch.Set(activeKey, value, nil); err != nil {
		return false, fmt.Errorf("webhook: stage outbox entry: %w", err)
	}
	if err := batch.Set(scheduledKey, nil, nil); err != nil {
		return false, fmt.Errorf("webhook: stage outbox schedule: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return false, fmt.Errorf("webhook: persist outbox entry: %w", err)
	}
	o.entries++
	o.logicalBytes += logicalSize
	o.signalLocked()
	return true, nil
}

func (o *DurableOutbox) hasLocked(key []byte) (bool, error) {
	_, closer, err := o.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("webhook: read outbox: %w", err)
	}
	if closer != nil {
		_ = closer.Close()
	}
	return true, nil
}

// ClaimDue reserves up to the configured batch size for local workers.
func (o *DurableOutbox) ClaimDue(now time.Time) ([]OutboxEntry, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, ErrOutboxClosed
	}
	iter, err := o.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(outboxSchedulePrefix),
		UpperBound: prefixEnd([]byte(outboxSchedulePrefix)),
	})
	if err != nil {
		return nil, fmt.Errorf("webhook: scan outbox: %w", err)
	}
	defer iter.Close()
	entries := make([]OutboxEntry, 0, o.opts.DispatchBatchSize)
	for valid := iter.First(); valid && len(entries) < o.opts.DispatchBatchSize; valid = iter.Next() {
		dueNS, id, err := parseOutboxScheduleKey(iter.Key())
		if err != nil {
			return nil, err
		}
		if dueNS > now.UnixNano() {
			break
		}
		if _, busy := o.inflight[id]; busy {
			continue
		}
		value, err := o.valueLocked([]byte(outboxActivePrefix + id))
		if err != nil {
			return nil, fmt.Errorf("webhook: resolve scheduled outbox entry: %w", err)
		}
		var entry OutboxEntry
		if err := json.Unmarshal(value, &entry); err != nil {
			return nil, fmt.Errorf("webhook: decode outbox entry: %w", err)
		}
		o.inflight[entry.ID] = struct{}{}
		entries = append(entries, entry)
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("webhook: scan outbox: %w", err)
	}
	return entries, nil
}

// MarkDelivered atomically replaces active work with a retained dedupe marker.
func (o *DurableOutbox) MarkDelivered(entry OutboxEntry, deliveredAt time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	defer delete(o.inflight, entry.ID)
	if o.closed {
		return ErrOutboxClosed
	}
	oldKey := []byte(outboxActivePrefix + entry.ID)
	oldScheduledKey := outboxScheduleKey(entry)
	oldValue, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	marker := deliveredMarker{ID: entry.ID, DeliveredAtNS: deliveredAt.UTC().UnixNano()}
	markerValue, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	newKey := []byte(outboxDeliveredPrefix + entry.ID)
	batch := o.db.NewBatch()
	defer batch.Close()
	if err := batch.Delete(oldKey, nil); err != nil {
		return err
	}
	if err := batch.Delete(oldScheduledKey, nil); err != nil {
		return err
	}
	if err := batch.Set(newKey, markerValue, nil); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("webhook: commit delivery receipt: %w", err)
	}
	o.entries--
	o.logicalBytes += int64(len(newKey)+len(markerValue)) - int64(len(oldKey)+len(oldValue)+len(oldScheduledKey))
	o.signalLocked()
	return nil
}

// MarkFailed schedules exponential retry or atomically moves work to dead-letter.
func (o *DurableOutbox) MarkFailed(entry OutboxEntry, maxAttempts int, now time.Time) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	defer delete(o.inflight, entry.ID)
	if o.closed {
		return false, ErrOutboxClosed
	}
	entry.Attempt++
	entry.LastErrorCode = "WEBHOOK_SEND_FAILED"
	oldValue, err := o.valueLocked([]byte(outboxActivePrefix + entry.ID))
	if err != nil {
		return false, err
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	dead := entry.Attempt >= maxAttempts
	oldKey := []byte(outboxActivePrefix + entry.ID)
	oldScheduledKey := outboxScheduleKey(entry)
	newKey := oldKey
	if dead {
		newKey = []byte(outboxDeadPrefix + entry.ID)
	} else {
		entry.NextAttemptNS = now.Add(o.retryDelay(entry.Attempt)).UTC().UnixNano()
	}
	newValue, err := json.Marshal(entry)
	if err != nil {
		return false, err
	}
	batch := o.db.NewBatch()
	defer batch.Close()
	if err := batch.Delete(oldScheduledKey, nil); err != nil {
		return false, err
	}
	if dead {
		if err := batch.Delete(oldKey, nil); err != nil {
			return false, err
		}
	}
	if err := batch.Set(newKey, newValue, nil); err != nil {
		return false, err
	}
	var newScheduledKey []byte
	if !dead {
		newScheduledKey = outboxScheduleKey(entry)
		if err := batch.Set(newScheduledKey, nil, nil); err != nil {
			return false, err
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return false, fmt.Errorf("webhook: commit retry state: %w", err)
	}
	o.logicalBytes += int64(len(newKey)+len(newValue)+len(newScheduledKey)) - int64(len(oldKey)+len(oldValue)+len(oldScheduledKey))
	o.signalLocked()
	return dead, nil
}

func (o *DurableOutbox) valueLocked(key []byte) ([]byte, error) {
	value, closer, err := o.db.Get(key)
	if err != nil {
		return nil, fmt.Errorf("webhook: read outbox value: %w", err)
	}
	defer closer.Close()
	return append([]byte(nil), value...), nil
}

func (o *DurableOutbox) retryDelay(attempt int) time.Duration {
	delay := o.opts.RetryBaseDelay
	for i := 1; i < attempt && delay < o.opts.RetryMaxDelay; i++ {
		if delay > o.opts.RetryMaxDelay/2 {
			return o.opts.RetryMaxDelay
		}
		delay *= 2
	}
	if delay > o.opts.RetryMaxDelay {
		return o.opts.RetryMaxDelay
	}
	return delay
}

// ReleaseClaim makes an interrupted item immediately eligible after restart/stop.
func (o *DurableOutbox) ReleaseClaim(id string) {
	o.mu.Lock()
	delete(o.inflight, id)
	o.signalLocked()
	o.mu.Unlock()
}

// ReplayDead atomically returns explicitly selected dead letters to pending.
func (o *DurableOutbox) ReplayDead(ctx context.Context, ids []string) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(ids) == 0 || len(ids) > 100 {
		return 0, fmt.Errorf("webhook: replay requires 1..100 ids")
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" || strings.TrimSpace(id) != id {
			return 0, fmt.Errorf("webhook: invalid replay id")
		}
		if _, exists := seen[id]; exists {
			return 0, fmt.Errorf("webhook: duplicate replay id")
		}
		seen[id] = struct{}{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return 0, ErrOutboxClosed
	}
	now := o.opts.Now().UTC().UnixNano()
	batch := o.db.NewBatch()
	defer batch.Close()
	replayed := 0
	var logicalDelta int64
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		oldKey := []byte(outboxDeadPrefix + id)
		value, err := o.valueLocked(oldKey)
		if errors.Is(err, pebble.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		var entry OutboxEntry
		if err := json.Unmarshal(value, &entry); err != nil {
			return 0, err
		}
		entry.Attempt = 0
		entry.NextAttemptNS = now
		entry.LastErrorCode = ""
		newValue, err := json.Marshal(entry)
		if err != nil {
			return 0, err
		}
		newKey := []byte(outboxActivePrefix + id)
		newScheduledKey := outboxScheduleKey(entry)
		if err := batch.Delete(oldKey, nil); err != nil {
			return 0, err
		}
		if err := batch.Set(newKey, newValue, nil); err != nil {
			return 0, err
		}
		if err := batch.Set(newScheduledKey, nil, nil); err != nil {
			return 0, err
		}
		logicalDelta += int64(len(newKey)+len(newValue)+len(newScheduledKey)) - int64(len(oldKey)+len(value))
		replayed++
	}
	if replayed == 0 {
		return 0, nil
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return 0, fmt.Errorf("webhook: commit dead-letter replay: %w", err)
	}
	o.logicalBytes += logicalDelta
	o.signalLocked()
	return replayed, nil
}

// Stats scans bounded metadata for readiness, metrics, and operator APIs.
func (o *DurableOutbox) Stats(ctx context.Context) (OutboxStats, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return OutboxStats{}, ErrOutboxClosed
	}
	stats := OutboxStats{LogicalBytes: o.logicalBytes}
	now := o.opts.Now().UTC()
	for _, prefix := range []string{outboxActivePrefix, outboxDeadPrefix, outboxDeliveredPrefix} {
		iter, err := o.db.NewIter(&pebble.IterOptions{
			LowerBound: []byte(prefix), UpperBound: prefixEnd([]byte(prefix)),
		})
		if err != nil {
			return OutboxStats{}, err
		}
		for valid := iter.First(); valid; valid = iter.Next() {
			if err := ctx.Err(); err != nil {
				_ = iter.Close()
				return OutboxStats{}, err
			}
			switch prefix {
			case outboxActivePrefix, outboxDeadPrefix:
				var entry OutboxEntry
				if err := json.Unmarshal(iter.Value(), &entry); err != nil {
					_ = iter.Close()
					return OutboxStats{}, err
				}
				if prefix == outboxActivePrefix {
					stats.Backlog++
				} else {
					stats.DeadLetters++
					if len(stats.DeadLetterEntries) < 100 {
						stats.DeadLetterEntries = append(stats.DeadLetterEntries, OutboxDeadLetter{
							ID: entry.ID, Event: entry.Event, Items: entry.Items, Attempt: entry.Attempt,
							CreatedAt: time.Unix(0, entry.CreatedAtNS).UTC(), LastErrorCode: entry.LastErrorCode,
						})
					}
				}
				stats.RetryAttempts += int64(entry.Attempt)
				created := time.Unix(0, entry.CreatedAtNS).UTC()
				if stats.OldestCreatedAt.IsZero() || created.Before(stats.OldestCreatedAt) {
					stats.OldestCreatedAt = created
				}
			case outboxDeliveredPrefix:
				stats.DeliveredTombstones++
			}
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return OutboxStats{}, err
		}
		_ = iter.Close()
	}
	if !stats.OldestCreatedAt.IsZero() {
		stats.OldestAge = now.Sub(stats.OldestCreatedAt)
		if stats.OldestAge < 0 {
			stats.OldestAge = 0
		}
	}
	return stats, nil
}

func (o *DurableOutbox) pruneDelivered(now time.Time) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return ErrOutboxClosed
	}
	threshold := now.Add(-o.opts.DeliveredRetention).UnixNano()
	iter, err := o.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(outboxDeliveredPrefix),
		UpperBound: prefixEnd([]byte(outboxDeliveredPrefix)),
	})
	if err != nil {
		return err
	}
	defer iter.Close()
	batch := o.db.NewBatch()
	defer batch.Close()
	removed := 0
	var logicalDelta int64
	for valid := iter.First(); valid; valid = iter.Next() {
		var marker deliveredMarker
		if err := json.Unmarshal(iter.Value(), &marker); err != nil {
			return err
		}
		if marker.DeliveredAtNS >= threshold {
			continue
		}
		key := append([]byte(nil), iter.Key()...)
		value := append([]byte(nil), iter.Value()...)
		if err := batch.Delete(key, nil); err != nil {
			return err
		}
		logicalDelta -= int64(len(key) + len(value))
		removed++
	}
	if removed == 0 {
		return nil
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}
	o.logicalBytes += logicalDelta
	return nil
}

func (o *DurableOutbox) recountLocked() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	iter, err := o.db.NewIter(nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for valid := iter.First(); valid; valid = iter.Next() {
		key := iter.Key()
		if strings.HasPrefix(string(key), outboxActivePrefix) ||
			strings.HasPrefix(string(key), outboxDeadPrefix) {
			o.entries++
		}
		o.logicalBytes += int64(len(iter.Key()) + len(iter.Value()))
	}
	return iter.Error()
}

func (o *DurableOutbox) signalLocked() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *DurableOutbox) Wake() <-chan struct{} { return o.wake }

func (o *DurableOutbox) Close() error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	db := o.db
	o.mu.Unlock()
	return db.Close()
}

func validateOutboxEntry(entry OutboxEntry) error {
	if entry.ID == "" || len(entry.ID) > 80 || strings.TrimSpace(entry.ID) != entry.ID ||
		(entry.Event != EventMsgNotify && entry.Event != EventMsgOffline) ||
		len(entry.Body) == 0 || entry.Items <= 0 {
		return fmt.Errorf("webhook: invalid outbox entry")
	}
	return nil
}

func prefixEnd(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] != 0xff {
			end[i]++
			return end[:i+1]
		}
	}
	return nil
}

func outboxScheduleKey(entry OutboxEntry) []byte {
	return []byte(fmt.Sprintf("%s%020d/%s", outboxSchedulePrefix, entry.NextAttemptNS, entry.ID))
}

func parseOutboxScheduleKey(key []byte) (int64, string, error) {
	value := strings.TrimPrefix(string(key), outboxSchedulePrefix)
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return 0, "", fmt.Errorf("webhook: invalid outbox schedule key")
	}
	dueNS, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || dueNS <= 0 {
		return 0, "", fmt.Errorf("webhook: invalid outbox schedule time")
	}
	return dueNS, parts[1], nil
}
