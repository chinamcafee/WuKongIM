package api

import (
	"context"
	"encoding/json"
	"net/http"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/gin-gonic/gin"
)

type syncMessageEventRequest struct {
	ChannelID       string `json:"channel_id"`
	ChannelType     uint8  `json:"channel_type"`
	FromUID         string `json:"from_uid"`
	ClientMsgNo     string `json:"client_msg_no"`
	EventKey        string `json:"event_key"`
	FromMsgEventSeq uint64 `json:"from_msg_event_seq"`
	Limit           int    `json:"limit"`
	IncludePrivate  uint8  `json:"include_private"`
}

func (s *Server) handleMessageEventSync(c *gin.Context) {
	var req syncMessageEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeJSONError(c, "数据格式有误！")
		return
	}
	reader, ok := s.messages.(interface {
		SyncMessageEvents(context.Context, messageusecase.MessageEventSyncQuery) (messageusecase.MessageEventSyncResult, error)
	})
	if !ok {
		writeJSONError(c, "message event sync usecase not configured")
		return
	}
	result, err := reader.SyncMessageEvents(c.Request.Context(), messageusecase.MessageEventSyncQuery{ChannelID: req.ChannelID, ChannelType: int64(req.ChannelType), FromUID: req.FromUID, ClientMsgNo: req.ClientMsgNo, EventKey: req.EventKey, FromMsgEventSeq: req.FromMsgEventSeq, Limit: req.Limit, IncludePrivate: req.IncludePrivate == 1})
	if err != nil {
		writeJSONError(c, err.Error())
		return
	}
	events := make([]map[string]any, 0, len(result.Events))
	for _, e := range result.Events {
		row := map[string]any{"msg_event_seq": e.Seq, "event_id": e.ID, "event_key": e.Key, "event_type": e.Type, "visibility": e.Visibility, "occurred_at": e.OccurredAt}
		if len(e.Payload) > 0 {
			var payload any
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				payload = string(e.Payload)
			}
			row["payload"] = payload
		}
		events = append(events, row)
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "data": gin.H{"client_msg_no": result.ClientMsgNo, "from_msg_event_seq": result.FromMsgEventSeq, "next_msg_event_seq": result.NextMsgEventSeq, "more": boolToInt(result.More), "events": events, "filtered_by_event_key": result.EventKey}})
}
