package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	cmdsyncusecase "github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/gin-gonic/gin"
)

const maxV3CommandAckCursors = 10000

const (
	internalCommandUIDHeader               = "X-LinkU-UID"
	internalCommandDeviceFlagHeader        = "X-LinkU-Device-Flag"
	internalCommandLoginSessionHeader      = "X-LinkU-Login-Session-ID"
	internalCommandCredentialVersionHeader = "X-LinkU-Credential-Version"
	internalCommandPrincipalContextKey     = "linku-command-principal"
)

type internalCommandPrincipal struct {
	UID               string
	DeviceFlag        uint8
	LoginSessionID    string
	CredentialVersion uint64
}

type v3CommandSyncRequest struct {
	Limit            int             `json:"limit"`
	ForbiddenUID     json.RawMessage `json:"uid"`
	ForbiddenFlag    json.RawMessage `json:"device_flag"`
	ForbiddenSession json.RawMessage `json:"login_session_id"`
	ForbiddenVersion json.RawMessage `json:"credential_version"`
}

type v3CommandAckCursor struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	ThroughSeq  uint64 `json:"through_seq"`
}

type v3CommandSyncResponse struct {
	BatchID     string               `json:"batch_id"`
	Messages    []legacyMessageResp  `json:"messages"`
	AckChannels []v3CommandAckCursor `json:"ack_channels"`
	More        int                  `json:"more"`
}

type v3CommandAckRequest struct {
	BatchID          string               `json:"batch_id"`
	AckChannels      []v3CommandAckCursor `json:"ack_channels"`
	ForbiddenUID     json.RawMessage      `json:"uid"`
	ForbiddenFlag    json.RawMessage      `json:"device_flag"`
	ForbiddenSession json.RawMessage      `json:"login_session_id"`
	ForbiddenVersion json.RawMessage      `json:"credential_version"`
}

func (s *Server) handleV3CommandSync(c *gin.Context) {
	var req v3CommandSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeJSONError(c, "invalid request")
		return
	}
	principal, ok := commandPrincipalFromContext(c)
	if !ok || req.Limit < 0 || hasForbiddenCommandIdentity(req.ForbiddenUID, req.ForbiddenFlag, req.ForbiddenSession, req.ForbiddenVersion) {
		writeJSONError(c, "invalid request")
		return
	}
	if s == nil || s.cmdSync == nil {
		writeJSONError(c, "cmd sync usecase not configured")
		return
	}
	result, err := s.cmdSync.BatchSync(c.Request.Context(), cmdsyncusecase.BatchSyncQuery{
		UID: principal.UID, DeviceFlag: principal.DeviceFlag,
		LoginSessionID: principal.LoginSessionID, CredentialVersion: principal.CredentialVersion,
		Limit: req.Limit,
	})
	if err != nil {
		writeCommandSyncError(c, err)
		return
	}
	response := v3CommandSyncResponse{
		BatchID:     result.BatchID,
		Messages:    make([]legacyMessageResp, 0, len(result.Messages)),
		AckChannels: make([]v3CommandAckCursor, 0, len(result.AckCursors)),
		More:        boolToInt(result.More),
	}
	for _, msg := range result.Messages {
		response.Messages = append(response.Messages, newLegacyMessageResp(principal.UID, cmdSyncMessageToLegacy(msg)))
	}
	for _, cursor := range result.AckCursors {
		response.AckChannels = append(response.AckChannels, v3CommandAckCursor{
			ChannelID: cursor.CommandChannelID, ChannelType: cursor.ChannelType, ThroughSeq: cursor.ThroughSeq,
		})
	}
	c.JSON(http.StatusOK, response)
}

func (s *Server) handleV3CommandAck(c *gin.Context) {
	var req v3CommandAckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeJSONError(c, "invalid request")
		return
	}
	principal, ok := commandPrincipalFromContext(c)
	batchID := strings.TrimSpace(req.BatchID)
	if !ok || !validV3CommandBatchID(batchID) || len(req.AckChannels) == 0 || len(req.AckChannels) > maxV3CommandAckCursors ||
		hasForbiddenCommandIdentity(req.ForbiddenUID, req.ForbiddenFlag, req.ForbiddenSession, req.ForbiddenVersion) {
		writeJSONError(c, "invalid request")
		return
	}
	if s == nil || s.cmdSync == nil {
		writeJSONError(c, "cmd sync usecase not configured")
		return
	}
	cursors := make([]cmdsyncusecase.AckCursor, 0, len(req.AckChannels))
	for _, cursor := range req.AckChannels {
		cursors = append(cursors, cmdsyncusecase.AckCursor{
			CommandChannelID: cursor.ChannelID,
			ChannelType:      cursor.ChannelType,
			ThroughSeq:       cursor.ThroughSeq,
		})
	}
	err := s.cmdSync.BatchAck(c.Request.Context(), cmdsyncusecase.BatchAckCommand{
		UID: principal.UID, DeviceFlag: principal.DeviceFlag,
		LoginSessionID: principal.LoginSessionID, CredentialVersion: principal.CredentialVersion,
		BatchID: batchID, AckCursors: cursors,
	})
	if err != nil {
		if errors.Is(err, cmdsyncusecase.ErrBatchIDMismatch) ||
			errors.Is(err, cmdsyncusecase.ErrAckCursorInvalid) ||
			errors.Is(err, cmdsyncusecase.ErrBatchIDRequired) ||
			errors.Is(err, cmdsyncusecase.ErrUIDRequired) {
			writeJSONError(c, "invalid command batch acknowledgment")
			return
		}
		writeCommandSyncError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
}

func validV3CommandBatchID(batchID string) bool {
	if len(batchID) != sha256HexLength {
		return false
	}
	_, err := hex.DecodeString(batchID)
	return err == nil
}

const sha256HexLength = 64

func (cursor *v3CommandAckCursor) UnmarshalJSON(data []byte) error {
	var raw struct {
		ChannelID   string          `json:"channel_id"`
		ChannelType uint8           `json:"channel_type"`
		ThroughSeq  json.RawMessage `json:"through_seq"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value := strings.TrimSpace(string(raw.ThroughSeq))
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if err := json.Unmarshal(raw.ThroughSeq, &value); err != nil {
			return err
		}
	}
	throughSeq, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return err
	}
	cursor.ChannelID = raw.ChannelID
	cursor.ChannelType = raw.ChannelType
	cursor.ThroughSeq = throughSeq
	return nil
}

func hasForbiddenCommandIdentity(values ...json.RawMessage) bool {
	for _, value := range values {
		if len(value) > 0 {
			return true
		}
	}
	return false
}

func (s *Server) requireCommandHMAC() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.internalCredentialHMACSecret == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "internal command authentication is not configured"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, internalCredentialMaxPayloadBytes+1))
		if err != nil || int64(len(body)) > internalCredentialMaxPayloadBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "command request too large"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		timestampText := strings.TrimSpace(c.GetHeader("X-LinkU-Timestamp"))
		nonce := strings.TrimSpace(c.GetHeader("X-LinkU-Nonce"))
		provided := strings.TrimSpace(c.GetHeader("X-LinkU-Signature"))
		uid := strings.TrimSpace(c.GetHeader(internalCommandUIDHeader))
		flagText := strings.TrimSpace(c.GetHeader(internalCommandDeviceFlagHeader))
		sessionID := strings.TrimSpace(c.GetHeader(internalCommandLoginSessionHeader))
		versionText := strings.TrimSpace(c.GetHeader(internalCommandCredentialVersionHeader))
		timestamp, timestampErr := strconv.ParseInt(timestampText, 10, 64)
		flag, flagErr := strconv.ParseUint(flagText, 10, 8)
		version, versionErr := strconv.ParseUint(versionText, 10, 64)
		now := time.Now()
		if timestampErr != nil || flagErr != nil || versionErr != nil || uid == "" || sessionID == "" || version == 0 ||
			nonce == "" || len(nonce) > 128 || !withinCredentialReplayWindow(now, timestamp, s.internalCredentialReplayWindow) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid internal command authentication"})
			return
		}
		bodyDigest := sha256.Sum256(body)
		canonical := internalCommandCanonical(c.Request.Method, c.Request.URL.Path, timestampText, nonce, uid, flagText, sessionID, versionText, hex.EncodeToString(bodyDigest[:]))
		mac := hmac.New(sha256.New, []byte(s.internalCredentialHMACSecret))
		_, _ = mac.Write([]byte(canonical))
		expected := hex.EncodeToString(mac.Sum(nil))
		if len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 ||
			!s.internalCredentialNonces.accept("cmd:"+nonce, now, s.internalCredentialReplayWindow) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid internal command authentication"})
			return
		}
		c.Set(internalCommandPrincipalContextKey, internalCommandPrincipal{
			UID: uid, DeviceFlag: uint8(flag), LoginSessionID: sessionID, CredentialVersion: version,
		})
		c.Next()
	}
}

func internalCommandCanonical(method, path, timestamp, nonce, uid, deviceFlag, sessionID, credentialVersion, bodyDigest string) string {
	return fmt.Sprintf("command-v1\n%s\n%s\n%s\n%s\n%d:%s\n%s\n%d:%s\n%s\n%s",
		method, path, timestamp, nonce, len(uid), uid, deviceFlag, len(sessionID), sessionID, credentialVersion, bodyDigest)
}

func commandPrincipalFromContext(c *gin.Context) (internalCommandPrincipal, bool) {
	if c == nil {
		return internalCommandPrincipal{}, false
	}
	value, ok := c.Get(internalCommandPrincipalContextKey)
	if !ok {
		return internalCommandPrincipal{}, false
	}
	principal, ok := value.(internalCommandPrincipal)
	return principal, ok
}

func writeCommandSyncError(c *gin.Context, err error) {
	if errors.Is(err, cmdsyncusecase.ErrPrincipalInvalid) || errors.Is(err, cmdsyncusecase.ErrPrincipalStale) ||
		errors.Is(err, cmdsyncusecase.ErrPrincipalStoreRequired) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "command principal is not current"})
		return
	}
	writeJSONError(c, err.Error())
}
