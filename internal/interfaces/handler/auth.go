package handler

import (
	"github.com/gin-gonic/gin"
	app "olixops-k8s-ctl/internal/application/auth"
	d "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/pkg/response"
)

// Token issuance and revocation are owned by the platform's personal settings.
func RegisterAuth(v1 *gin.RouterGroup, _ *app.Service) {
	v1.GET("/auth/me", func(c *gin.Context) {
		p, ok := d.FromContext(c.Request.Context())
		if !ok {
			response.AbortWithHTTPStatus(c, 401)
			return
		}
		c.Header("Cache-Control", "no-store")
		response.Success(c, p)
	})
}
