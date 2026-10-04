package sendauthorization

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client owns a fixed signed callback and rejects pressure without a waiting queue.
type Client struct {
	endpoint string
	path     string
	secret   []byte
	client   *http.Client
	permits  chan struct{}
}

// New validates deployment settings; unencrypted HTTP is restricted to loopback.
func New(endpoint, secret string, timeout time.Duration, maxConcurrent int) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Path == "" || parsed.RawPath != "" {
		return nil, errors.New("personal send authorization URL is invalid")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")) {
		return nil, errors.New("personal send authorization requires HTTPS or loopback HTTP")
	}
	if len(secret) < 32 || timeout <= 0 || timeout > 5*time.Second || maxConcurrent < 1 || maxConcurrent > 4096 {
		return nil, errors.New("personal send authorization settings are invalid")
	}
	return &Client{endpoint: endpoint, path: parsed.EscapedPath(), secret: []byte(secret), permits: make(chan struct{}, maxConcurrent),
		client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Authorize makes a fresh fail-closed decision; no positive cache extends account eligibility.
func (c *Client) Authorize(ctx context.Context, sender, receiver string, recipientIsSystem bool) (bool, error) {
	select {
	case c.permits <- struct{}{}:
		defer func() { <-c.permits }()
	default:
		return false, errors.New("personal send authorization saturated")
	}
	body, err := json.Marshal(struct {
		Sender    string `json:"senderUid"`
		Recipient string `json:"recipientUid"`
		System    bool   `json:"recipientIsSystem"`
	}{sender, receiver, recipientIsSystem})
	if err != nil {
		return false, err
	}
	timestamp, nonce := strconv.FormatInt(time.Now().Unix(), 10), uuid.NewString()
	digest := sha256.Sum256(body)
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(strings.Join([]string{"POST", c.path, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n")))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LinkU-Timestamp", timestamp)
	request.Header.Set("X-LinkU-Nonce", nonce)
	request.Header.Set("X-LinkU-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := c.client.Do(request)
	if err != nil {
		return false, errors.New("personal send authorization transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, errors.New("personal send authorization rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return false, errors.New("personal send authorization response invalid")
	}
	var result struct {
		Success *bool  `json:"success"`
		Code    string `json:"code"`
		Data    *struct {
			Allowed *bool `json:"allowed"`
		} `json:"data"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || result.Success == nil || !*result.Success || result.Code != "SUCCESS" || result.Data == nil || result.Data.Allowed == nil {
		return false, errors.New("personal send authorization response invalid")
	}
	return *result.Data.Allowed, nil
}
