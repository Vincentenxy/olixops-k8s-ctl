package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	app "olixops-k8s-ctl/internal/application/auth"
	d "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/pkg/response"
)

func RegisterAuth(v1 *gin.RouterGroup, s *app.Service) {
	a := v1.Group("/auth")
	a.GET("/me", func(c *gin.Context) {
		p, ok := d.FromContext(c.Request.Context())
		if !ok {
			response.AbortWithHTTPStatus(c, 401)
			return
		}
		c.Header("Cache-Control", "no-store")
		response.Success(c, p)
	})
	tokens := v1.Group("/user/token")
	tokens.POST("/create", func(c *gin.Context) {
		var in app.CreateInput
		if !bindSmallJSON(c, &in) {
			return
		}
		out, e := s.Create(c.Request.Context(), in)
		authResult(c, out, e)
	})
	tokens.GET("/list", func(c *gin.Context) { out, e := s.List(c.Request.Context()); authResult(c, out, e) })
	tokens.POST("/delete", func(c *gin.Context) {
		var in struct {
			ID string `json:"id"`
		}
		if !bindSmallJSON(c, &in) {
			return
		}
		e := s.Revoke(c.Request.Context(), in.ID)
		authResult(c, struct{}{}, e)
	})
}
func authResult(c *gin.Context, out any, e error) {
	c.Header("Cache-Control", "no-store")
	if e == nil {
		response.Success(c, out)
		return
	}
	status := 500
	switch {
	case errors.Is(e, d.ErrUnauthenticated):
		status = 401
	case errors.Is(e, d.ErrForbidden):
		status = 403
	case errors.Is(e, d.ErrInvalid):
		status = 400
	case errors.Is(e, d.ErrNotFound):
		status = 404
	case errors.Is(e, d.ErrLimit):
		status = 409
	}
	response.AbortWithHTTPStatus(c, status)
}
