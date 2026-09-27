package middleware

import (
	"github.com/gin-gonic/gin"

	"olixops-k8s-ctl/pkg/response"
)

const ControlKeyHeader = "X-Olixops-Control-Key"

func ControlKey(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if expected != "" && c.GetHeader(ControlKeyHeader) != expected {
			response.AbortWithHTTPStatus(c, 401)
			return
		}
		c.Next()
	}
}
