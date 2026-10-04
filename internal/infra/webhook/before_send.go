package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

// EventBeforeSend names the synchronous business admission webhook.
const EventBeforeSend = "msg.before_send"

// BeforeSendResponseMaxBytes bounds JSON decoding, including the Base64 payload.
const BeforeSendResponseMaxBytes = 64 << 10

// BeforeSendClient performs a single HTTP callback without redirects or retries.
// The caller owns the deadline, concurrency limit, and business failure policy.
type BeforeSendClient struct {
	target string
	client *http.Client
}

// NewBeforeSendClient validates the configured endpoint and isolates client policy.
// A supplied transport is useful for deterministic tests and managed connections.
func NewBeforeSendClient(addr string, transport http.RoundTripper) (*BeforeSendClient, error) {
	target, err := url.Parse(addr)
	if err != nil || target == nil || (target.Scheme != "http" && target.Scheme != "https") ||
		target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return nil, errors.New("before-send webhook requires an absolute HTTP(S) URL without userinfo or fragment")
	}
	query := target.Query()
	query.Set("event", EventBeforeSend)
	target.RawQuery = query.Encode()
	return &BeforeSendClient{
		target: target.String(),
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// BeforeSend encodes an uncommitted message and strictly decodes an explicit decision.
func (c *BeforeSendClient) BeforeSend(ctx context.Context, req message.BeforeSendRequest) (message.BeforeSendDecision, error) {
	body, err := json.Marshal(struct {
		FromUID     string `json:"from_uid"`
		ChannelID   string `json:"channel_id"`
		ChannelType uint8  `json:"channel_type"`
		ClientMsgNo string `json:"client_msg_no"`
		Payload     []byte `json:"payload"`
		NoPersist   bool   `json:"no_persist"`
		SyncOnce    bool   `json:"sync_once"`
	}{req.FromUID, req.ChannelID, req.ChannelType, req.ClientMsgNo, req.Payload, req.NoPersist, req.SyncOnce})
	if err != nil {
		return message.BeforeSendDecision{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.target, bytes.NewReader(body))
	if err != nil {
		return message.BeforeSendDecision{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return message.BeforeSendDecision{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return message.BeforeSendDecision{}, errors.New("before-send webhook returned non-200 status")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, BeforeSendResponseMaxBytes+1))
	if err != nil {
		return message.BeforeSendDecision{}, err
	}
	if len(data) > BeforeSendResponseMaxBytes {
		return message.BeforeSendDecision{}, errors.New("before-send webhook response too large")
	}

	// Decode one object with unique, allowlisted keys before interpreting its decision.
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return message.BeforeSendDecision{}, errors.New("invalid before-send webhook response")
	}
	fields := make(map[string]json.RawMessage, 3)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return message.BeforeSendDecision{}, errors.New("invalid before-send webhook response")
		}
		key, ok := token.(string)
		if !ok || (key != "allow" && key != "payload" && key != "reason_code") {
			return message.BeforeSendDecision{}, errors.New("unknown before-send webhook field")
		}
		if _, duplicate := fields[key]; duplicate {
			return message.BeforeSendDecision{}, errors.New("duplicate before-send webhook field")
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return message.BeforeSendDecision{}, errors.New("invalid before-send webhook field")
		}
		fields[key] = value
	}
	if _, err = decoder.Token(); err != nil || decoder.Decode(new(any)) != io.EOF {
		return message.BeforeSendDecision{}, errors.New("before-send webhook requires one decision")
	}
	var allow *bool
	if json.Unmarshal(fields["allow"], &allow) != nil || allow == nil {
		return message.BeforeSendDecision{}, errors.New("before-send webhook requires explicit allow")
	}
	decision := message.BeforeSendDecision{Allow: *allow}
	if !decision.Allow {
		// An explicit denial remains terminal even when optional fields are invalid.
		if _, hasPayload := fields["payload"]; !hasPayload {
			_ = json.Unmarshal(fields["reason_code"], &decision.ReasonCode)
		}
		return decision, nil
	}
	if value, ok := fields["payload"]; ok {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &decision.Payload) != nil {
			return message.BeforeSendDecision{}, errors.New("invalid before-send payload")
		}
	}
	if value, ok := fields["reason_code"]; ok {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &decision.ReasonCode) != nil {
			return message.BeforeSendDecision{}, errors.New("invalid before-send reason")
		}
	}
	return decision, nil
}
