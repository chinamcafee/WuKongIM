package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/gin-gonic/gin"
)

const internalCredentialMaxPayloadBytes int64 = 1024 * 1024

const (
	internalCredentialApplyPath  = "/internal/v3/device-credentials:apply-batch"
	internalCredentialRevokePath = "/internal/v3/device-credentials:revoke-batch"
)

type applyDeviceCredentialItem struct {
	UID               string `json:"uid"`
	DeviceFlag        uint8  `json:"deviceFlag"`
	CredentialStatus  string `json:"credentialStatus"`
	Token             string `json:"token"`
	CredentialVersion uint64 `json:"credentialVersion"`
	LoginSessionID    string `json:"loginSessionId"`
	ExpiresAt         int64  `json:"expiresAt"`
	OperationID       string `json:"operationId"`
	OperationKind     string `json:"operationKind"`
	ReplacementCause  string `json:"replacementCause"`
}

type revokeDeviceCredentialItem struct {
	UID               string `json:"uid"`
	DeviceFlag        uint8  `json:"deviceFlag"`
	CredentialVersion uint64 `json:"credentialVersion"`
	LoginSessionID    string `json:"loginSessionId"`
	OperationID       string `json:"operationId"`
	TerminationCause  string `json:"terminationCause"`
}

type applyDeviceCredentialRequest struct {
	Items []applyDeviceCredentialItem `json:"items"`
}

type revokeDeviceCredentialRequest struct {
	Items []revokeDeviceCredentialItem `json:"items"`
}

func (s *Server) registerDeviceCredentialRoutes() {
	if s == nil || s.engine == nil {
		return
	}
	s.engine.PUT(internalCredentialApplyPath, s.requireCredentialHMAC(), s.handleApplyDeviceCredentials)
	s.engine.POST(internalCredentialRevokePath, s.requireCredentialHMAC(), s.handleRevokeDeviceCredentials)
}

func (s *Server) handleApplyDeviceCredentials(c *gin.Context) {
	if s.deviceCredentials == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "credential service unavailable"})
		return
	}
	var request applyDeviceCredentialRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Items) == 0 || len(request.Items) > s.internalCredentialMaxBatchSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid credential batch"})
		return
	}
	commands := make([]userusecase.ApplyDeviceCredentialCommand, len(request.Items))
	for i, item := range request.Items {
		if item.CredentialStatus != "ACTIVE" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "credentialStatus must be ACTIVE"})
			return
		}
		commands[i] = userusecase.ApplyDeviceCredentialCommand{
			UID: item.UID, DeviceFlag: protocolmeta.DeviceFlag(item.DeviceFlag), Token: item.Token,
			CredentialVersion: item.CredentialVersion, LoginSessionID: item.LoginSessionID,
			ExpiresAtUnixMS: item.ExpiresAt, OperationID: item.OperationID,
			OperationKind: item.OperationKind, ReplacementCause: item.ReplacementCause,
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": s.deviceCredentials.ApplyDeviceCredentials(c.Request.Context(), commands)})
}

func (s *Server) handleRevokeDeviceCredentials(c *gin.Context) {
	if s.deviceCredentials == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "credential service unavailable"})
		return
	}
	var request revokeDeviceCredentialRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Items) == 0 || len(request.Items) > s.internalCredentialMaxBatchSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid credential batch"})
		return
	}
	commands := make([]userusecase.RevokeDeviceCredentialCommand, len(request.Items))
	for i, item := range request.Items {
		commands[i] = userusecase.RevokeDeviceCredentialCommand{
			UID: item.UID, DeviceFlag: protocolmeta.DeviceFlag(item.DeviceFlag),
			CredentialVersion: item.CredentialVersion, LoginSessionID: item.LoginSessionID,
			OperationID: item.OperationID, TerminationCause: item.TerminationCause,
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": s.deviceCredentials.RevokeDeviceCredentials(c.Request.Context(), commands)})
}

func (s *Server) requireCredentialHMAC() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.internalCredentialHMACSecret == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "internal credential authentication is not configured"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, internalCredentialMaxPayloadBytes+1))
		if err != nil || int64(len(body)) > internalCredentialMaxPayloadBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "credential request too large"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		timestampText := strings.TrimSpace(c.GetHeader("X-LinkU-Timestamp"))
		nonce := strings.TrimSpace(c.GetHeader("X-LinkU-Nonce"))
		provided := strings.TrimSpace(c.GetHeader("X-LinkU-Signature"))
		timestamp, parseErr := strconv.ParseInt(timestampText, 10, 64)
		now := time.Now()
		if parseErr != nil || nonce == "" || len(nonce) > 128 || !withinCredentialReplayWindow(now, timestamp, s.internalCredentialReplayWindow) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid internal credential authentication"})
			return
		}
		bodyDigest := sha256.Sum256(body)
		canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", c.Request.Method, c.Request.URL.Path, timestampText, nonce, hex.EncodeToString(bodyDigest[:]))
		mac := hmac.New(sha256.New, []byte(s.internalCredentialHMACSecret))
		_, _ = mac.Write([]byte(canonical))
		expected := hex.EncodeToString(mac.Sum(nil))
		if len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 ||
			!s.internalCredentialNonces.accept(nonce, now, s.internalCredentialReplayWindow) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid internal credential authentication"})
			return
		}
		c.Next()
	}
}

func withinCredentialReplayWindow(now time.Time, unixSeconds int64, window time.Duration) bool {
	delta := now.Sub(time.Unix(unixSeconds, 0))
	return delta >= -window && delta <= window
}

type credentialNonceCache struct {
	mu      sync.Mutex
	maxSize int
	seen    map[string]time.Time
}

func newCredentialNonceCache(maxSize int) *credentialNonceCache {
	return &credentialNonceCache{maxSize: maxSize, seen: make(map[string]time.Time)}
}

func (c *credentialNonceCache) accept(nonce string, now time.Time, window time.Duration) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, observed := range c.seen {
		if now.Sub(observed) > window {
			delete(c.seen, key)
		}
	}
	if _, exists := c.seen[nonce]; exists {
		return false
	}
	if c.maxSize > 0 && len(c.seen) >= c.maxSize {
		return false
	}
	c.seen[nonce] = now
	return true
}
