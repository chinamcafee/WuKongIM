package api

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	cmdsyncusecase "github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/gin-gonic/gin"
)

const maxV3CommandAckCursors = 10000

type v3CommandSyncRequest struct {
	UID   string `json:"uid"`
	Limit int    `json:"limit"`
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
	UID         string               `json:"uid"`
	BatchID     string               `json:"batch_id"`
	AckChannels []v3CommandAckCursor `json:"ack_channels"`
}

func (s *Server) handleV3CommandSync(c *gin.Context) {
	var req v3CommandSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeJSONError(c, "invalid request")
		return
	}
	uid := strings.TrimSpace(req.UID)
	if uid == "" || req.Limit < 0 {
		writeJSONError(c, "invalid request")
		return
	}
	if s == nil || s.cmdSync == nil {
		writeJSONError(c, "cmd sync usecase not configured")
		return
	}
	result, err := s.cmdSync.BatchSync(c.Request.Context(), cmdsyncusecase.BatchSyncQuery{
		UID: uid, Limit: req.Limit,
	})
	if err != nil {
		writeJSONError(c, err.Error())
		return
	}
	response := v3CommandSyncResponse{
		BatchID:     result.BatchID,
		Messages:    make([]legacyMessageResp, 0, len(result.Messages)),
		AckChannels: make([]v3CommandAckCursor, 0, len(result.AckCursors)),
		More:        boolToInt(result.More),
	}
	for _, msg := range result.Messages {
		response.Messages = append(response.Messages, newLegacyMessageResp(uid, cmdSyncMessageToLegacy(msg)))
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
	uid := strings.TrimSpace(req.UID)
	batchID := strings.TrimSpace(req.BatchID)
	if uid == "" || !validV3CommandBatchID(batchID) || len(req.AckChannels) == 0 || len(req.AckChannels) > maxV3CommandAckCursors {
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
		UID: uid, BatchID: batchID, AckCursors: cursors,
	})
	if err != nil {
		if errors.Is(err, cmdsyncusecase.ErrBatchIDMismatch) ||
			errors.Is(err, cmdsyncusecase.ErrAckCursorInvalid) ||
			errors.Is(err, cmdsyncusecase.ErrBatchIDRequired) ||
			errors.Is(err, cmdsyncusecase.ErrUIDRequired) {
			writeJSONError(c, "invalid command batch acknowledgment")
			return
		}
		writeJSONError(c, err.Error())
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
