package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
	Data    any    `json:"data"`
}

func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Code: 0, Message: "success", Data: data})
}

func AbortWithHTTPStatus(c *gin.Context, status int) {
	reason := map[int]string{
		400: "invalid_argument", 401: "unauthenticated", 403: "forbidden",
		404: "not_found", 405: "method_not_allowed", 408: "canceled",
		409: "conflict", 413: "request_too_large", 429: "rate_limited",
		500: "internal_error", 502: "upstream_unavailable", 503: "upstream_unavailable", 504: "deadline_exceeded",
	}[status]
	if reason == "" {
		reason = "request_failed"
	}
	c.AbortWithStatusJSON(status, Response{Code: status, Message: http.StatusText(status), Data: gin.H{"reason": reason}})
}
