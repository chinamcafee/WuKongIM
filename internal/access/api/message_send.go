package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics/tracectx"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)

type sendMessageRequest struct {
	FromUID          string                   `json:"from_uid"`
	LegacyFromUID    string                   `json:"sender_uid"`
	DeviceID         string                   `json:"device_id"`
	ChannelID        string                   `json:"channel_id"`
	ChannelType      uint8                    `json:"channel_type"`
	ClientMsgNo      string                   `json:"client_msg_no"`
	Setting          uint8                    `json:"setting"`
	Topic            string                   `json:"topic"`
	Expire           uint32                   `json:"expire"`
	Payload          string                   `json:"payload"`
	Subscribers      []string                 `json:"subscribers"`
	Header           sendMessageHeaderRequest `json:"header"`
	NoPersist        int                      `json:"no_persist"`
	SyncOnce         int                      `json:"sync_once"`
	WaitForPersist   int                      `json:"wait_for_persist"`
	PersistTimeoutMS int                      `json:"persist_timeout_ms"`
}

const (
	defaultPersistTimeoutMS = 3000
	minPersistTimeoutMS     = 100
	maxPersistTimeoutMS     = 10000
)

type sendMessageHeaderRequest struct {
	// NoPersist marks the send as non-durable when non-zero.
	NoPersist int `json:"no_persist"`
	// SyncOnce marks the send as a one-shot command-channel message when non-zero.
	SyncOnce int `json:"sync_once"`
	// RedDot preserves the durable notification framing flag when non-zero.
	RedDot int `json:"red_dot"`
}

type sendMessageResponse struct {
	MessageID  int64  `json:"message_id"`
	MessageSeq uint64 `json:"message_seq"`
	Reason     uint8  `json:"reason"`
}

func (s *Server) registerMessageRoutes() {
	if s == nil || s.engine == nil {
		return
	}
	s.engine.POST("/message/send", s.handleSendMessage)
	s.engine.POST("/message/event", s.handleMessageEventAppend)
	s.engine.POST("/message/sync", s.handleMessageSync)
	s.engine.POST("/message/syncack", s.handleMessageSyncAck)
	s.engine.POST("/channel/messagesync", s.handleChannelMessageSync)
}

func (s *Server) handleSendMessage(c *gin.Context) {
	var req sendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeSendJSONError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if req.FromUID == "" {
		req.FromUID = req.LegacyFromUID
	}
	requestScoped := len(req.Subscribers) > 0
	if req.FromUID == "" || req.Payload == "" {
		writeSendJSONError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if requestScoped {
		if req.ChannelID != "" {
			writeSendJSONError(c, http.StatusBadRequest, "invalid request")
			return
		}
	} else if req.ChannelID == "" || req.ChannelType == 0 {
		writeSendJSONError(c, http.StatusBadRequest, "invalid request")
		return
	}
	noPersist := req.Header.NoPersist != 0 || req.NoPersist != 0
	if !noPersist && strings.TrimSpace(req.ClientMsgNo) == "" {
		writePersistError(c, http.StatusBadRequest, "client_msg_no_required", "持久消息的client_msg_no不能为空", false)
		return
	}
	if req.WaitForPersist != 0 && req.WaitForPersist != 1 {
		writeSendJSONError(c, http.StatusBadRequest, "wait_for_persist must be 0 or 1")
		return
	}
	if req.WaitForPersist == 0 && req.PersistTimeoutMS != 0 {
		writeSendJSONError(c, http.StatusBadRequest, "persist_timeout_ms requires wait_for_persist=1")
		return
	}
	if req.WaitForPersist == 1 && (noPersist || requestScoped) {
		writeSendJSONError(c, http.StatusBadRequest, "this message mode cannot wait for persistence")
		return
	}
	if req.WaitForPersist == 1 {
		if req.PersistTimeoutMS == 0 {
			req.PersistTimeoutMS = defaultPersistTimeoutMS
		}
		if req.PersistTimeoutMS < minPersistTimeoutMS || req.PersistTimeoutMS > maxPersistTimeoutMS {
			writeSendJSONError(c, http.StatusBadRequest, fmt.Sprintf("persist_timeout_ms must be between %d and %d", minPersistTimeoutMS, maxPersistTimeoutMS))
			return
		}
	}

	payload, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		writeSendJSONError(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if s == nil || s.messages == nil {
		writeSendJSONError(c, http.StatusInternalServerError, "message usecase not configured")
		return
	}

	reqCtx := c.Request.Context()
	if traceID, ok := tracectx.ValidateHeaderTraceID(c.GetHeader("X-WK-Trace-ID")); ok {
		reqCtx = tracectx.WithContext(reqCtx, tracectx.Context{TraceID: traceID, Sampled: true})
	}
	reqCtx, traceCtx := tracectx.Ensure(reqCtx, nil)
	syncOnce := req.Header.SyncOnce != 0 || req.SyncOnce != 0
	cmd := messageusecase.SendCommand{
		TraceID:                traceCtx.TraceID,
		FromUID:                req.FromUID,
		DeviceID:               req.DeviceID,
		ChannelID:              req.ChannelID,
		ChannelType:            req.ChannelType,
		ClientMsgNo:            req.ClientMsgNo,
		Setting:                req.Setting,
		Topic:                  req.Topic,
		Expire:                 req.Expire,
		Payload:                payload,
		NoPersist:              noPersist,
		SyncOnce:               syncOnce,
		RedDot:                 req.Header.RedDot != 0,
		NormalizePersonChannel: req.ChannelType == frame.ChannelTypePerson,
		ProtocolVersion:        frame.LatestVersion,
	}
	if requestScoped {
		cmd.ChannelID = ""
		cmd.ChannelType = 0
		cmd.RequestScoped = true
		cmd.MessageScopedUIDs = append([]string(nil), req.Subscribers...)
	}
	if req.WaitForPersist == 1 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(reqCtx, time.Duration(req.PersistTimeoutMS)*time.Millisecond)
		defer cancel()
	}

	result, err := s.messages.Send(reqCtx, cmd)
	if err != nil {
		if errors.Is(err, messageusecase.ErrIdempotencyConflict) {
			writePersistError(c, http.StatusConflict, "idempotency_conflict", "client_msg_no已被不同消息使用", false)
			return
		}
		if req.WaitForPersist == 1 && errors.Is(err, context.DeadlineExceeded) {
			writePersistError(c, http.StatusGatewayTimeout, "persist_timeout", "消息持久化确认超时", true)
			return
		}
		if status, msg, ok := mapSendError(err); ok {
			writeSendJSONError(c, status, msg)
			return
		}
		if req.WaitForPersist == 1 {
			writePersistError(c, http.StatusServiceUnavailable, "persist_failed", "消息持久化失败", true)
			return
		}
		writeSendJSONError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if req.WaitForPersist == 1 {
		if result.Reason != messageusecase.ReasonSuccess {
			writePersistReasonError(c, result.Reason)
			return
		}
		deduplicated := 0
		if result.Deduplicated {
			deduplicated = 1
		}
		c.JSON(http.StatusOK, gin.H{
			"status":        http.StatusOK,
			"message_id":    fmt.Sprintf("%d", result.MessageID),
			"message_seq":   result.MessageSeq,
			"client_msg_no": req.ClientMsgNo,
			"deduplicated":  deduplicated,
		})
		return
	}
	c.JSON(http.StatusOK, sendMessageResponse{
		MessageID:  int64(result.MessageID),
		MessageSeq: result.MessageSeq,
		Reason:     uint8(mapMessageReason(result.Reason)),
	})
}

func writePersistReasonError(c *gin.Context, reason messageusecase.Reason) {
	mapped := mapMessageReason(reason)
	status := http.StatusForbidden
	code := "send_rejected"
	message := "消息未通过发送权限检查"
	retryable := false
	if mapped == frame.ReasonSystemError || mapped == frame.ReasonNodeNotMatch {
		status = http.StatusServiceUnavailable
		code = "send_unavailable"
		message = "消息发送服务暂时不可用"
		retryable = true
	} else if mapped == frame.ReasonPayloadDecodeError {
		status = http.StatusBadRequest
		code = "send_invalid"
		message = "消息发送请求无效"
	}
	c.JSON(status, gin.H{
		"status": status, "code": code, "msg": message,
		"retryable": retryable, "reason": uint8(mapped),
	})
}

func writePersistError(c *gin.Context, status int, code string, message string, retryable bool) {
	c.JSON(status, gin.H{"status": status, "code": code, "msg": message, "retryable": retryable})
}

func writeSendJSONError(c *gin.Context, status int, message string) {
	if c == nil {
		return
	}
	if message == "" {
		message = http.StatusText(status)
	}
	c.JSON(status, gin.H{"error": message})
}
