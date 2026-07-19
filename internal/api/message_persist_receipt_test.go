package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/persistreceipt"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRespondPersistReceiptSuccessAndDeduplicated(t *testing.T) {
	response := recordPersistResponse(t, func(c *wkhttp.Context) {
		respondPersistReceipt(c, persistreceipt.Result{
			MessageID:    101,
			MessageSeq:   7,
			ClientMsgNo:  "relation-event-1",
			Deduplicated: true,
		})
	})
	require.Equal(t, http.StatusOK, response.Code)
	body := decodeResponseBody(t, response)
	require.Equal(t, "101", body["message_id"])
	require.Equal(t, float64(7), body["message_seq"])
	require.Equal(t, float64(1), body["deduplicated"])
}

func TestRespondPersistReceiptConflict(t *testing.T) {
	response := recordPersistResponse(t, func(c *wkhttp.Context) {
		respondPersistReceipt(c, persistreceipt.Result{ErrorCode: persistreceipt.CodeIdempotencyConflict})
	})
	require.Equal(t, http.StatusConflict, response.Code)
	body := decodeResponseBody(t, response)
	require.Equal(t, persistreceipt.CodeIdempotencyConflict, body["code"])
	require.Equal(t, false, body["retryable"])
}

func TestAwaitPersistReceiptTimeoutIsRetryable(t *testing.T) {
	response := recordPersistResponse(t, func(c *wkhttp.Context) {
		awaitPersistReceipt(c, make(chan persistreceipt.Result), time.Millisecond)
	})
	require.Equal(t, http.StatusGatewayTimeout, response.Code)
	body := decodeResponseBody(t, response)
	require.Equal(t, "persist_timeout", body["code"])
	require.Equal(t, true, body["retryable"])
}

func recordPersistResponse(t *testing.T, handle func(*wkhttp.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/message/send", nil)
	handle(&wkhttp.Context{Context: ginContext})
	return recorder
}

func decodeResponseBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}
