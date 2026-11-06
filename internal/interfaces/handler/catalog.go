package handler

import (
	"context"
	"olixops-k8s-ctl/internal/application/api"
	"time"

	"github.com/gin-gonic/gin"
	app "olixops-k8s-ctl/internal/application/catalog"
)

// RegisterCatalog uses the same application methods as the MCP tools. The caller
// supplies an API-key-protected /api/v1 group; permissions remain a policy seam.
func RegisterCatalog(v1 *gin.RouterGroup, s *app.Service, budgets ...time.Duration) {
	budget := 120 * time.Second
	if len(budgets) > 0 && budgets[0] > 0 {
		budget = budgets[0]
	}
	templates := v1.Group("/templates")
	{
		templates.GET("", catalogRead(func(ctx context.Context) (any, error) { return s.Templates(ctx) }))
		templates.POST("/get", catalogCall(s.Template))
		templates.POST("/save", operation(budget, s.SaveTemplate))
	}

	services := v1.Group("/services")
	{
		services.GET("/standard", catalogRead(func(ctx context.Context) (any, error) { return s.StandardServices(ctx) }))
		services.POST("/deploy-standard", operation(budget, s.DeployStandard))
		services.GET("", catalogRead(func(ctx context.Context) (any, error) { return s.Instances(ctx, app.ListInput{}) }))
		services.POST("/list", catalogCall(s.Instances))
		services.POST("/get", catalogCall(s.Instance))
		services.POST("/save-config", operation(budget, s.SaveConfig))
		services.POST("/preview-config", catalogCall(s.PreviewConfig))
		services.POST("/revisions", catalogCall(s.Revisions))
		services.POST("/prepare-deployment", operation(budget, s.Prepare))
		services.POST("/deploy", operation(budget, s.Deploy))
		services.POST("/deployment", catalogCall(s.Deployment))
		services.POST("/verify-access", catalogCall(s.VerifyAccess))
		services.POST("/deployments", catalogCall(s.Deployments))
		services.POST("/delete", operation(budget, s.DeleteInstance))
	}

	ports := v1.Group("/ports")
	{
		ports.GET("", catalogRead(func(ctx context.Context) (any, error) { return s.Ports(ctx, app.PortInput{Limit: 500}) }))
		ports.POST("/list", catalogCall(s.Ports))
		ports.POST("/sync", func(c *gin.Context) {
			// Legacy callers may omit the empty body; supplied JSON still follows the MCP contract.
			if c.Request.ContentLength != 0 {
				if _, err := api.Decode[api.EmptyInput](c.Request.Body); err != nil {
					operationResult(c, nil, err)
					return
				}
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), budget)
			defer cancel()
			out, err := s.SyncPorts(ctx)
			operationResult(c, out, err)
		})
		ports.POST("/reserve", operation(budget, s.ReservePort))
		ports.POST("/release", operation(budget, s.ReleasePort))
		ports.POST("/events", catalogCall(s.PortEvents))
	}
}
func catalogCall[In, Out any](call func(context.Context, In) (Out, error)) gin.HandlerFunc {
	return operation(api.ReadTimeout, call)
}
func catalogRead(call func(context.Context) (any, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), api.ReadTimeout)
		defer cancel()
		out, err := call(ctx)
		operationResult(c, out, err)
	}
}
