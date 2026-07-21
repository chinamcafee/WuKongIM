package webhook

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

const (
	webhookNotifyQueue  = "webhook_notify"
	webhookOfflineQueue = "webhook_offline"
	webhookOnlineQueue  = "webhook_online_status"

	resultAccepted       = "accepted"
	resultOK             = "ok"
	resultFull           = "full"
	resultClosed         = "closed"
	resultCanceled       = "canceled"
	resultTimeout        = "timeout"
	resultError          = "error"
	resultRetry          = "retry"
	resultRetryExhausted = "retry_exhausted"
	resultEncodeError    = "encode_error"
	resultDeduplicated   = "deduplicated"
	resultDeadLetter     = "dead_letter"
	resultOutboxError    = "outbox_error"
)

type runtimeState uint8

const (
	runtimeStateNew runtimeState = iota
	runtimeStateStarted
	runtimeStateStopping
	runtimeStateStopped
)

// RuntimeOptions configures the bounded webhook runtime.
type RuntimeOptions struct {
	// Sender delivers encoded webhook requests.
	Sender Sender
	// Observer receives low-cardinality runtime observations.
	Observer Observer
	// QueueSize bounds best-effort online-status events waiting in memory.
	QueueSize int
	// Workers bounds concurrent sender calls per event queue.
	Workers int
	// OnlineBatchMaxItems limits user.onlinestatus records sent in one request.
	OnlineBatchMaxItems int
	// OnlineBatchMaxWait bounds how long user.onlinestatus waits for adjacent records.
	OnlineBatchMaxWait time.Duration
	// OfflineUIDBatchSize is the compression threshold used for msg.offline UID chunks.
	OfflineUIDBatchSize int
	// RequestTimeout bounds one outbound sender attempt.
	RequestTimeout time.Duration
	// RetryMaxAttempts moves critical delivery to dead-letter and ends best-effort delivery.
	RetryMaxAttempts int
	// FocusEvents limits delivered event names. Empty means all events are delivered.
	FocusEvents []string
	// Outbox configures sync-WAL storage for msg.notify and msg.offline.
	Outbox OutboxOptions
}

// Runtime owns durable critical-webhook and bounded presence-event delivery.
type Runtime struct {
	opts     RuntimeOptions
	sender   Sender
	observer Observer
	focus    map[string]struct{}

	mu             sync.RWMutex
	state          runtimeState
	stopCh         chan struct{}
	online         *workqueue.BoundedBatchPool[OnlineStatus]
	outbox         *DurableOutbox
	dispatchCancel context.CancelFunc
	dispatchWG     sync.WaitGroup
}

// New creates a webhook runtime. Start opens the queues.
func New(opts RuntimeOptions) (*Runtime, error) {
	if opts.Sender == nil || opts.QueueSize <= 0 || opts.Workers <= 0 {
		return nil, workqueue.ErrInvalidConfig
	}
	if opts.OnlineBatchMaxWait < 0 || opts.RequestTimeout <= 0 {
		return nil, workqueue.ErrInvalidConfig
	}
	if opts.OnlineBatchMaxItems < 0 || opts.OfflineUIDBatchSize < 0 {
		return nil, workqueue.ErrInvalidConfig
	}
	if opts.OnlineBatchMaxItems == 0 {
		opts.OnlineBatchMaxItems = 1
	}
	rt := &Runtime{
		opts:     opts,
		sender:   opts.Sender,
		observer: opts.Observer,
		focus:    make(map[string]struct{}, len(opts.FocusEvents)),
		stopCh:   make(chan struct{}),
	}
	for _, event := range opts.FocusEvents {
		if event != "" {
			rt.focus[event] = struct{}{}
		}
	}
	if rt.enabled(EventMsgNotify) || rt.enabled(EventMsgOffline) {
		outbox, err := OpenDurableOutbox(opts.Outbox)
		if err != nil {
			return nil, err
		}
		rt.outbox = outbox
	}
	return rt, nil
}

// Start opens bounded queue admission.
func (r *Runtime) Start(ctx context.Context) error {
	if r == nil {
		return workqueue.ErrInvalidConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.state {
	case runtimeStateStarted:
		return nil
	case runtimeStateStopping, runtimeStateStopped:
		return workqueue.ErrClosed
	}
	r.ensureStopChLocked()

	online, err := workqueue.NewBoundedBatchPool[OnlineStatus](workqueue.BoundedBatchPoolConfig[OnlineStatus]{
		Name:      webhookOnlineQueue,
		Workers:   r.opts.Workers,
		QueueSize: r.opts.QueueSize,
		Policy: func(OnlineStatus) workqueue.BatchOptions {
			return workqueue.BatchOptions{MaxItems: r.opts.OnlineBatchMaxItems, MaxWait: r.opts.OnlineBatchMaxWait}
		},
	}, r.handleOnlineBatch)
	if err != nil {
		return err
	}
	r.online = online
	dispatchCtx, dispatchCancel := context.WithCancel(context.Background())
	r.dispatchCancel = dispatchCancel
	if r.outbox != nil {
		r.dispatchWG.Add(1)
		go r.runOutboxMaintenance(dispatchCtx)
		for worker := 0; worker < r.opts.Workers; worker++ {
			r.dispatchWG.Add(1)
			go r.runOutboxDispatcher(dispatchCtx)
		}
	}
	r.state = runtimeStateStarted
	return nil
}

// Stop closes admission and drains accepted webhook work until ctx expires.
func (r *Runtime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	switch r.state {
	case runtimeStateNew:
		r.state = runtimeStateStopped
		close(r.ensureStopChLocked())
		outbox := r.outbox
		r.mu.Unlock()
		if outbox != nil {
			return outbox.Close()
		}
		return nil
	case runtimeStateStopped:
		r.mu.Unlock()
		return nil
	case runtimeStateStopping:
		stopCh := r.ensureStopChLocked()
		r.mu.Unlock()
		return waitForStop(ctx, stopCh)
	}
	r.state = runtimeStateStopping
	online := r.online
	dispatchCancel := r.dispatchCancel
	stopCh := r.ensureStopChLocked()
	r.mu.Unlock()

	if dispatchCancel != nil {
		dispatchCancel()
	}
	dispatchDone := make(chan struct{})
	go func() {
		r.dispatchWG.Wait()
		close(dispatchDone)
	}()
	var dispatchErr error
	select {
	case <-ctx.Done():
		dispatchErr = ctx.Err()
	case <-dispatchDone:
	}
	err := errors.Join(online.Close(ctx), dispatchErr)
	if r.outbox != nil {
		err = errors.Join(err, r.outbox.Close())
	}

	r.mu.Lock()
	r.online = nil
	r.dispatchCancel = nil
	r.state = runtimeStateStopped
	close(stopCh)
	r.mu.Unlock()
	return err
}

// Notify admits one committed message for msg.notify delivery.
func (r *Runtime) Notify(ctx context.Context, msg Message) {
	if r == nil || !r.enabled(EventMsgNotify) {
		return
	}
	msg = cloneMessage(msg)
	body, err := buildNotifyBody([]Message{msg})
	if err != nil {
		r.observeSend(EventMsgNotify, resultEncodeError, 1, 0, 0, err)
		return
	}
	r.mu.RLock()
	started := r.state == runtimeStateStarted
	outbox := r.outbox
	r.mu.RUnlock()
	if !started || outbox == nil {
		r.observeAdmission(EventMsgNotify, webhookNotifyQueue, resultClosed, 1, 0, r.opts.QueueSize, nil)
		return
	}
	inserted, err := outbox.Enqueue(ctx, OutboxEntry{
		ID: StableEventID(EventMsgNotify, msg, nil), Event: EventMsgNotify,
		Body: body, Items: 1,
	})
	if err != nil {
		r.observeAdmission(EventMsgNotify, webhookNotifyQueue, resultOutboxError, 1, 0, r.opts.Outbox.MaxEntries, err)
		return
	}
	result := resultAccepted
	if !inserted {
		result = resultDeduplicated
	}
	r.observeAdmission(EventMsgNotify, webhookNotifyQueue, result, 1, 0, r.opts.Outbox.MaxEntries, nil)
}

// Offline admits one bounded recipient chunk for msg.offline delivery.
func (r *Runtime) Offline(ctx context.Context, msg OfflineMessage) {
	if r == nil || !r.enabled(EventMsgOffline) {
		return
	}
	msg = cloneOfflineMessage(msg)
	items := len(msg.ToUIDs)
	if items == 0 {
		return
	}
	body, err := buildOfflineBody(msg, r.opts.OfflineUIDBatchSize)
	if err != nil {
		r.observeSend(EventMsgOffline, resultEncodeError, items, 0, 0, err)
		return
	}
	r.mu.RLock()
	started := r.state == runtimeStateStarted
	outbox := r.outbox
	r.mu.RUnlock()
	if !started || outbox == nil {
		r.observeAdmission(EventMsgOffline, webhookOfflineQueue, resultClosed, items, 0, r.opts.QueueSize, nil)
		return
	}
	inserted, err := outbox.Enqueue(ctx, OutboxEntry{
		ID: StableEventID(EventMsgOffline, msg.Message, msg.ToUIDs), Event: EventMsgOffline,
		Body: body, Items: items,
	})
	if err != nil {
		r.observeAdmission(EventMsgOffline, webhookOfflineQueue, resultOutboxError, items, 0, r.opts.Outbox.MaxEntries, err)
		return
	}
	result := resultAccepted
	if !inserted {
		result = resultDeduplicated
	}
	r.observeAdmission(EventMsgOffline, webhookOfflineQueue, result, items, 0, r.opts.Outbox.MaxEntries, nil)
}

// OnlineStatus admits one legacy-compatible status record.
func (r *Runtime) OnlineStatus(ctx context.Context, status OnlineStatus) {
	if r == nil || !r.enabled(EventUserOnlineStatus) || status.Value == "" {
		return
	}

	r.mu.RLock()
	online := r.online
	if r.state != runtimeStateStarted || online == nil {
		r.mu.RUnlock()
		r.observeAdmission(EventUserOnlineStatus, webhookOnlineQueue, resultClosed, 1, 0, r.opts.QueueSize, nil)
		return
	}
	err := online.Submit(ctx, status)
	depth := online.QueueDepth()
	capacity := online.QueueCapacity()
	r.mu.RUnlock()
	r.observeAdmission(EventUserOnlineStatus, webhookOnlineQueue, admissionResult(err), 1, depth, capacity, err)
}

func (r *Runtime) handleOnlineBatch(ctx context.Context, batch []OnlineStatus) error {
	body, err := buildOnlineStatusBody(batch)
	if err != nil {
		r.observeSend(EventUserOnlineStatus, resultEncodeError, len(batch), 0, 0, err)
		return nil
	}
	r.sendWithRetry(ctx, EventUserOnlineStatus, body, len(batch))
	return nil
}

func (r *Runtime) runOutboxDispatcher(ctx context.Context) {
	defer r.dispatchWG.Done()
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.outbox.Wake():
		case <-poll.C:
		}
		entries, err := r.outbox.ClaimDue(time.Now())
		if err != nil {
			if errors.Is(err, ErrOutboxClosed) || ctx.Err() != nil {
				return
			}
			r.observeSend("outbox", resultOutboxError, 0, 0, 0, err)
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				r.outbox.ReleaseClaim(entry.ID)
				continue
			}
			r.dispatchOutboxEntry(ctx, entry)
		}
	}
}

func (r *Runtime) runOutboxMaintenance(ctx context.Context) {
	defer r.dispatchWG.Done()
	statsTicker := time.NewTicker(5 * time.Second)
	pruneTicker := time.NewTicker(time.Minute)
	defer statsTicker.Stop()
	defer pruneTicker.Stop()
	r.refreshOutboxStats()
	for {
		select {
		case <-ctx.Done():
			return
		case <-statsTicker.C:
			r.refreshOutboxStats()
		case now := <-pruneTicker.C:
			if err := r.outbox.pruneDelivered(now); err != nil && !errors.Is(err, ErrOutboxClosed) {
				r.observeSend("outbox", resultOutboxError, 0, 0, 0, err)
			}
			r.refreshOutboxStats()
		}
	}
}

func (r *Runtime) dispatchOutboxEntry(ctx context.Context, entry OutboxEntry) {
	attemptCtx, cancel := context.WithTimeout(ctx, r.opts.RequestTimeout)
	started := time.Now()
	err := r.sender.Send(attemptCtx, SendRequest{
		ID: entry.ID, Event: entry.Event, Body: entry.Body, Attempt: entry.Attempt + 1,
	})
	cancel()
	duration := time.Since(started)
	if err == nil {
		if markErr := r.outbox.MarkDelivered(entry, time.Now()); markErr != nil {
			r.observeSend(entry.Event, resultOutboxError, entry.Items, entry.Attempt+1, duration, markErr)
			return
		}
		r.observeSend(entry.Event, resultOK, entry.Items, entry.Attempt+1, duration, nil)
		return
	}
	if ctx.Err() != nil {
		r.outbox.ReleaseClaim(entry.ID)
		return
	}
	dead, markErr := r.outbox.MarkFailed(entry, r.opts.RetryMaxAttempts, time.Now())
	if markErr != nil {
		r.observeSend(entry.Event, resultOutboxError, entry.Items, entry.Attempt+1, duration, markErr)
		return
	}
	result := resultRetry
	if dead {
		result = resultDeadLetter
	}
	r.observeSend(entry.Event, result, entry.Items, entry.Attempt+1, duration, err)
}

// OutboxStats exposes critical webhook backlog and dead-letter state.
func (r *Runtime) OutboxStats(ctx context.Context) (OutboxStats, error) {
	if r == nil || r.outbox == nil {
		return OutboxStats{}, ErrOutboxClosed
	}
	return r.outbox.Stats(ctx)
}

// ReplayDeadLetters requeues an explicit, bounded set of stable webhook IDs.
func (r *Runtime) ReplayDeadLetters(ctx context.Context, ids []string) (int, error) {
	if r == nil || r.outbox == nil {
		return 0, ErrOutboxClosed
	}
	replayed, err := r.outbox.ReplayDead(ctx, ids)
	if err == nil {
		r.refreshOutboxStats()
	}
	return replayed, err
}

func (r *Runtime) refreshOutboxStats() {
	if r == nil || r.outbox == nil {
		return
	}
	stats, err := r.outbox.Stats(context.Background())
	if err == nil {
		r.observeOutboxStats(stats)
	}
}

func (r *Runtime) observeOutboxStats(stats OutboxStats) {
	if r == nil || r.observer == nil {
		return
	}
	r.observer.ObserveWebhook(Observation{
		Queue: webhookNotifyQueue, Event: "outbox", Result: "snapshot",
		OutboxSnapshot: true, Backlog: stats.Backlog, DeadLetters: stats.DeadLetters,
		OldestAge: stats.OldestAge, LogicalBytes: stats.LogicalBytes,
		RetryAttempts: stats.RetryAttempts,
	})
}

func (r *Runtime) sendWithRetry(ctx context.Context, event string, body []byte, items int) {
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := r.opts.RetryMaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			r.observeSend(event, contextResult(err), items, attempt, 0, err)
			return
		}
		attemptCtx := ctx
		cancel := func() {}
		if r.opts.RequestTimeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, r.opts.RequestTimeout)
		}
		started := time.Now()
		err := r.sender.Send(attemptCtx, SendRequest{Event: event, Body: body})
		cancel()
		duration := time.Since(started)
		if err == nil {
			r.observeSend(event, resultOK, items, attempt, duration, nil)
			return
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			r.observeSend(event, contextResult(ctxErr), items, attempt, duration, ctxErr)
			return
		}
		if attempt == attempts {
			r.observeSend(event, resultRetryExhausted, items, attempt, duration, err)
			return
		}
		r.observeSend(event, resultRetry, items, attempt, duration, err)
	}
}

func (r *Runtime) enabled(event string) bool {
	if r == nil {
		return false
	}
	if len(r.focus) == 0 {
		return true
	}
	_, ok := r.focus[event]
	return ok
}

func (r *Runtime) ensureStopChLocked() chan struct{} {
	if r.stopCh == nil {
		r.stopCh = make(chan struct{})
	}
	return r.stopCh
}

func (r *Runtime) observeAdmission(event string, queue string, result string, items int, depth int, size int, err error) {
	if r == nil || r.observer == nil {
		return
	}
	r.observer.ObserveWebhook(Observation{
		Queue:      queue,
		Event:      event,
		Result:     result,
		Items:      items,
		QueueDepth: depth,
		QueueSize:  size,
		Err:        err,
	})
}

func (r *Runtime) observeSend(event string, result string, items int, attempt int, duration time.Duration, err error) {
	if r == nil || r.observer == nil {
		return
	}
	r.observer.ObserveWebhook(Observation{
		Queue:    queueForEvent(event),
		Event:    event,
		Result:   result,
		Items:    items,
		Attempt:  attempt,
		Duration: duration,
		Err:      err,
	})
}

func queueForEvent(event string) string {
	switch event {
	case EventMsgNotify:
		return webhookNotifyQueue
	case EventMsgOffline:
		return webhookOfflineQueue
	case EventUserOnlineStatus:
		return webhookOnlineQueue
	default:
		return ""
	}
}

func admissionResult(err error) string {
	switch {
	case err == nil:
		return resultAccepted
	case errors.Is(err, workqueue.ErrFull):
		return resultFull
	case errors.Is(err, workqueue.ErrClosed):
		return resultClosed
	case errors.Is(err, context.Canceled):
		return resultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return resultTimeout
	default:
		return resultError
	}
}

func contextResult(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return resultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return resultTimeout
	default:
		return resultError
	}
}

func cloneMessage(msg Message) Message {
	msg.Payload = append([]byte(nil), msg.Payload...)
	return msg
}

func cloneOfflineMessage(msg OfflineMessage) OfflineMessage {
	msg.Message = cloneMessage(msg.Message)
	msg.ToUIDs = append([]string(nil), msg.ToUIDs...)
	return msg
}

func waitForStop(ctx context.Context, stopCh <-chan struct{}) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-stopCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
