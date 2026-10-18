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
	api := r.Group("/api")
	v1 := api.Group("/v1")
	pub := v1.Group("/pub")
	pub.GET("/health", health.Health)
	pub.GET("/ready", health.Ready)
	if len(services) > 0 && services[0] != nil {
		cluster := handler.NewClusterHandler(services[0])
		registerMCP(pub, cfg, services[0])
		registerClusterRoutes(v1, cfg, cluster)
	}

	return r, nil
}

func registerMCP(pub *gin.RouterGroup, cfg *config.Config, service *application.ClusterService) {
	if cfg.MCP.Enabled {
		mcpHandler := mcpiface.NewHandler(service, cfg.App.Version, mcpiface.Options{WriteEnabled: cfg.MCP.WriteEnabled, WriteTimeout: time.Duration(cfg.Kubernetes.CommandTimeoutSecond) * time.Second})
		pub.Any("/mcp", func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), logger.TraceIDKey, c.GetString(string(logger.TraceIDKey)))
			mcpHandler.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
		})
	}
}

func registerClusterRoutes(v1 *gin.RouterGroup, cfg *config.Config, clusterHandler *handler.ClusterHandler) {
	cluster := v1.Group("/cluster", middleware.ControlKey(cfg.Control.APIKey))

	cluster.GET("/status", clusterHandler.Status)

	namespaces := cluster.Group("/namespaces")
	namespaces.GET("", clusterHandler.Namespaces)
	namespace := namespaces.Group("/:namespace")
	namespace.GET("/resources", clusterHandler.Resources)
	namespace.GET("/gateway-resources", clusterHandler.GatewayResources)

	storageClasses := cluster.Group("/storage-classes")
	storageClasses.GET("", clusterHandler.StorageClasses)

	kubectl := cluster.Group("/kubectl")
	kubectl.POST("/apply", clusterHandler.Apply)

	helm := cluster.Group("/helm")
	helm.POST("/upgrade", clusterHandler.HelmUpgrade)
}
