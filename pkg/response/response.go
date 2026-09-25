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
	c.AbortWithStatusJSON(status, Response{Code: status, Message: http.StatusText(status), Data: nil})
}
