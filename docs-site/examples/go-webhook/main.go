// Command go-webhook demonstrates msg.before_send decisions on a loopback HTTP server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	maxRequestBytes    = 64 << 10
	maxPayloadBytes    = 32767
	maxInFlight        = 64
	businessRejectCode = 200
)

// beforeSendRequest mirrors the outbound callback, whose payload is Base64 in JSON.
// ClientMsgNo may be empty; this example has no side effects or deduplication store.
type beforeSendRequest struct {
	FromUID     string `json:"from_uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	ClientMsgNo string `json:"client_msg_no"`
	Payload     []byte `json:"payload"`
	NoPersist   bool   `json:"no_persist"`
	SyncOnce    bool   `json:"sync_once"`
}

// decision changes only Payload. Nil Payload means preserve the original bytes.
// ReasonCode is a SENDACK/business reason, not an HTTP status code.
type decision struct {
	Allow      bool   `json:"allow"`
	Payload    []byte `json:"payload,omitempty"`
	ReasonCode uint8  `json:"reason_code,omitempty"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "loopback IP and port to listen on")
	flag.Parse()
	logger := log.New(os.Stdout, "webhook-example ", log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *addr, logger); err != nil {
		logger.Fatal(err)
	}
}

// run owns the listener and drains accepted requests when the process is stopped.
func run(ctx context.Context, addr string, logger *log.Logger) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("-addr must use a loopback IP, such as 127.0.0.1:8090")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           newHandler(logger),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Printf("listening on http://%s/webhook", listener.Addr())
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		if err != nil {
			_ = server.Close()
		}
		<-done
		return err
	}
}

// newHandler bounds concurrent body decoding and returns explicit business decisions.
func newHandler(logger *log.Logger) http.Handler {
	slots := make(chan struct{}, maxInFlight)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Query().Get("event") != "msg.before_send" {
			http.Error(w, "unsupported event", http.StatusBadRequest)
			return
		}
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "callback capacity reached", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request beforeSendRequest
		err = decoder.Decode(&request)
		if err == nil {
			if trailingErr := decoder.Decode(new(any)); trailingErr != io.EOF {
				err = trailingErr
				if err == nil {
					err = errors.New("multiple JSON values")
				}
			}
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			status := http.StatusBadRequest
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, "invalid callback JSON or body size", status)
			return
		}
		if strings.TrimSpace(request.FromUID) == "" || strings.TrimSpace(request.ChannelID) == "" || request.ChannelType == 0 || len(request.Payload) == 0 || len(request.Payload) > maxPayloadBytes {
			http.Error(w, "invalid callback fields or payload size", http.StatusBadRequest)
			return
		}
		result := evaluate(request.Payload)
		w.Header().Set("Content-Type", "application/json")
		// Explicit denial still returns HTTP 200, so on_error=allow cannot bypass it.
		if err := json.NewEncoder(w).Encode(result); err != nil {
			return
		}
		action := "allow"
		if !result.Allow {
			action = "reject"
		} else if result.Payload != nil {
			action = "replace"
		}
		logger.Printf("decision=%s", action) // Never log identities, tokens, or message content.
	})
	return mux
}

// evaluate is the business customization point. It is pure and deterministic:
// retries of the same input produce the same decision without unbounded caches.
func evaluate(payload []byte) decision {
	var object map[string]json.RawMessage
	var text string
	isJSONText := false
	if json.Valid(payload) {
		// Only the documented type=1 text format is rewritten. Other message types pass through.
		var kind int
		if json.Unmarshal(payload, &object) != nil || json.Unmarshal(object["type"], &kind) != nil || kind != 1 || json.Unmarshal(object["content"], &text) != nil {
			return decision{Allow: true}
		}
		isJSONText = true
	} else {
		if !utf8.Valid(payload) {
			return decision{Allow: true}
		}
		text = string(payload)
	}
	if strings.HasPrefix(text, "[reject]") {
		return decision{Allow: false, ReasonCode: businessRejectCode}
	}
	if !strings.HasPrefix(text, "[replace]") {
		return decision{Allow: true}
	}
	replacement := "Reviewed: " + strings.TrimSpace(strings.TrimPrefix(text, "[replace]"))
	output := []byte(replacement)
	if isJSONText {
		// RawMessage preserves extra fields, including integers beyond JavaScript's safe range.
		object["content"], _ = json.Marshal(replacement)
		var err error
		output, err = json.Marshal(object)
		if err != nil {
			return decision{Allow: false, ReasonCode: businessRejectCode}
		}
	}
	if len(output) > maxPayloadBytes {
		return decision{Allow: false, ReasonCode: businessRejectCode}
	}
	return decision{Allow: true, Payload: output}
}
