package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	app "olixops-k8s-ctl/internal/application/catalog"
	d "olixops-k8s-ctl/internal/domain/catalog"
	"olixops-k8s-ctl/pkg/logger"
	"olixops-k8s-ctl/pkg/response"
)

// RegisterCatalog uses the same application methods as the MCP tools. The caller
// supplies an API-key-protected /api/v1 group; permissions remain a policy seam.
func RegisterCatalog(v1 *gin.RouterGroup, s *app.Service) {
	templates := v1.Group("/templates")
	templates.GET("", catalogRead(func(ctx context.Context) (any, error) { return s.Templates(ctx) }))
	templates.POST("/get", catalogCall(s.Template))
	templates.POST("/save", catalogCall(s.SaveTemplate))
	services := v1.Group("/services")
	services.GET("", catalogRead(func(ctx context.Context) (any, error) { return s.Instances(ctx, app.ListInput{}) }))
	services.POST("/list", catalogCall(s.Instances))
	services.POST("/get", catalogCall(s.Instance))
	services.POST("/save-config", catalogCall(s.SaveConfig))
	services.POST("/preview-config", catalogCall(s.PreviewConfig))
	services.POST("/revisions", catalogCall(s.Revisions))
	services.POST("/prepare-deployment", catalogCall(s.Prepare))
	services.POST("/deploy", catalogCall(s.Deploy))
	services.POST("/deployment", catalogCall(s.Deployment))
	services.POST("/verify-access", catalogCall(s.VerifyAccess))
	services.POST("/deployments", catalogCall(s.Deployments))
	services.POST("/delete", catalogCall(s.DeleteInstance))
	ports := v1.Group("/ports")
	ports.GET("", catalogRead(func(ctx context.Context) (any, error) { return s.Ports(ctx, app.PortInput{Limit: 500}) }))
	ports.POST("/list", catalogCall(s.Ports))
	ports.POST("/sync", catalogRead(func(ctx context.Context) (any, error) { return s.SyncPorts(ctx) }))
	ports.POST("/reserve", catalogCall(s.ReservePort))
	ports.POST("/release", catalogCall(s.ReleasePort))
	ports.POST("/events", catalogCall(s.PortEvents))
}
func catalogCall[In, Out any](call func(context.Context, In) (Out, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		dec := json.NewDecoder(c.Request.Body)
		dec.DisallowUnknownFields()
		var in In
		if err := dec.Decode(&in); err != nil {
			response.AbortWithHTTPStatus(c, 400)
			return
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			response.AbortWithHTTPStatus(c, 400)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithValue(c.Request.Context(), logger.TraceIDKey, c.GetString(string(logger.TraceIDKey))), 120*time.Second)
		defer cancel()
		out, err := call(ctx, in)
		catalogResult(c, out, err)
	}
}
func catalogRead(call func(context.Context) (any, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(context.WithValue(c.Request.Context(), logger.TraceIDKey, c.GetString(string(logger.TraceIDKey))), 120*time.Second)
		defer cancel()
		out, err := call(ctx)
		catalogResult(c, out, err)
	}
}
func catalogResult(c *gin.Context, out any, err error) {
	if err == nil {
		response.Success(c, out)
		return
	}
	var business *d.Error
	if errors.As(err, &business) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 400, "msg": business.Message, "data": business})
		return
	}
	response.AbortWithHTTPStatus(c, http.StatusInternalServerError)
}
