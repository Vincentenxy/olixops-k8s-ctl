package router

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/interfaces/handler"
	mcpiface "olixops-k8s-ctl/internal/interfaces/mcp"
	"olixops-k8s-ctl/internal/interfaces/middleware"
	"olixops-k8s-ctl/pkg/logger"
)

func New(cfg *config.Config, services ...*application.ClusterService) (*gin.Engine, error) {
	gin.SetMode(cfg.Server.Mode)
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.GinLogger(), gin.Recovery())

	health := handler.NewHealthHandler(cfg.App.Name, cfg.App.Version)
	v1 := r.Group("/api/v1")
	pub := v1.Group("/pub")
	pub.GET("/health", health.Health)
	pub.GET("/ready", health.Ready)
	if len(services) > 0 && services[0] != nil {
		if cfg.MCP.Enabled {
			mcpHandler := mcpiface.NewHandler(services[0], cfg.App.Version, mcpiface.Options{WriteEnabled: cfg.MCP.WriteEnabled, WriteTimeout: time.Duration(cfg.Kubernetes.CommandTimeoutSecond) * time.Second})
			pub.Any("/mcp", func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), logger.TraceIDKey, c.GetString(string(logger.TraceIDKey)))
				mcpHandler.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
			})
		}
		cluster := handler.NewClusterHandler(services[0])
		control := v1.Group("/cluster", middleware.ControlKey(cfg.Control.APIKey))
		control.GET("/status", cluster.Status)
		control.GET("/namespaces", cluster.Namespaces)
		control.GET("/storage-classes", cluster.StorageClasses)
		control.GET("/namespaces/:namespace/resources", cluster.Resources)
		control.GET("/namespaces/:namespace/gateway-resources", cluster.GatewayResources)
		control.POST("/kubectl/apply", cluster.Apply)
		control.POST("/helm/upgrade", cluster.HelmUpgrade)
	}

	return r, nil
}
