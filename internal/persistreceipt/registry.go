package persistreceipt

import "sync"

const (
	CodeSuccess             = ""
	CodeClientMsgNoRequired = "client_msg_no_required"
	CodeIdempotencyConflict = "idempotency_conflict"
	CodePersistFailed       = "persist_failed"
	CodePersistRejected     = "persist_rejected"
)

type Result struct {
	MessageID    int64
	MessageSeq   uint64
	ClientMsgNo  string
	Deduplicated bool
	ErrorCode    string
	ReasonCode   uint8
}

type Registry struct {
	mu      sync.Mutex
	waiters map[string]chan Result
}

func NewRegistry() *Registry {
	return &Registry{waiters: make(map[string]chan Result)}
}

var defaultRegistry = NewRegistry()

func Register(requestID string) <-chan Result {
	return defaultRegistry.Register(requestID)
}

func Resolve(requestID string, result Result) bool {
	return defaultRegistry.Resolve(requestID, result)
}

func Cancel(requestID string) {
	defaultRegistry.Cancel(requestID)
}

func (r *Registry) Register(requestID string) <-chan Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	waiter := make(chan Result, 1)
	r.waiters[requestID] = waiter
	return waiter
}

func (r *Registry) Resolve(requestID string, result Result) bool {
	if requestID == "" {
		return false
	}
	r.mu.Lock()
	waiter, exists := r.waiters[requestID]
	if exists {
		delete(r.waiters, requestID)
	}
	r.mu.Unlock()
	if !exists {
		return false
	}
	waiter <- result
	return true
}

func (r *Registry) Cancel(requestID string) {
	r.mu.Lock()
	delete(r.waiters, requestID)
	r.mu.Unlock()
}

func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.waiters)
}
