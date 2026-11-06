package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	d "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/pkg/response"
)

type Authenticator interface {
	Authenticate(context.Context, string) (d.Principal, error)
}

// Authenticate never trusts identity headers, cookies, body fields or query tokens.
func Authenticate(s Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		headers := c.Request.Header.Values("Authorization")
		var parts []string
		if len(headers) == 1 {
			parts = strings.Fields(headers[0])
		}
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 16384 {
			unauthorized(c)
			return
		}
		p, err := s.Authenticate(c.Request.Context(), parts[1])
		if err != nil {
			if errors.Is(err, d.ErrUnauthenticated) {
				unauthorized(c)
			} else {
				response.AbortWithHTTPStatus(c, 503)
			}
			return
		}
		c.Request = c.Request.WithContext(d.WithPrincipal(c.Request.Context(), p))
		c.Next()
	}
}
func unauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer realm="k8s-ctl"`)
	response.AbortWithHTTPStatus(c, http.StatusUnauthorized)
}
