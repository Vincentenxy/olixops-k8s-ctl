package router

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"olixops-k8s-ctl/internal/application"
	authapp "olixops-k8s-ctl/internal/application/auth"
	catalogapp "olixops-k8s-ctl/internal/application/catalog"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/interfaces/handler"
	mcpiface "olixops-k8s-ctl/internal/interfaces/mcp"
	"olixops-k8s-ctl/internal/interfaces/middleware"
	"olixops-k8s-ctl/pkg/logger"
	"olixops-k8s-ctl/pkg/response"
)

func New(cfg *config.Config, services ...*application.ClusterService) (*gin.Engine, error) {
	return newRouter(cfg, nil, nil, services...)
}

func NewWithCatalog(cfg *config.Config, service *application.ClusterService, catalog *catalogapp.Service) (*gin.Engine, error) {
	return newRouter(cfg, catalog, nil, service)
}

func NewWithAuth(cfg *config.Config, service *application.ClusterService, catalog *catalogapp.Service, auth *authapp.Service) (*gin.Engine, error) {
	return newRouter(cfg, catalog, auth, service)
}
func newRouter(cfg *config.Config, catalog *catalogapp.Service, auth *authapp.Service, services ...*application.ClusterService) (*gin.Engine, error) {
	if err := cfg.ValidatePodExec(); err != nil {
		return nil, err
	}
	if cfg.MCP.DiagnosticsOnly && (len(services) == 0 || services[0] == nil || !services[0].DiagnosticAccessConfigured()) {
		return nil, fmt.Errorf("diagnostics-only requires application diagnostic grants")
	}
	if cfg.Auth.Enabled && auth == nil {
		return nil, fmt.Errorf("authentication service required")
	}
	gin.SetMode(cfg.Server.Mode)
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.GinLogger(), func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(http.ErrAbortHandler)
				}
				logger.Error(c.Request.Context(), "http panic recovered")
				response.AbortWithHTTPStatus(c, 500)
			}
		}()
		c.Next()
	})
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) { response.AbortWithHTTPStatus(c, 405) })
	r.NoRoute(func(c *gin.Context) {
		response.AbortWithHTTPStatus(c, 404)
	})

	health := handler.NewHealthHandler(cfg.App.Name, cfg.App.Version)
	api := r.Group("/api")
	{
		v1 := api.Group("/v1")
		{
			pub := v1.Group("/pub")
			{
				pub.GET("/health", health.Health)
				pub.GET("/ready", health.Ready)
				if !cfg.Auth.Enabled && len(services) > 0 && services[0] != nil {
					registerMCP(pub, cfg, services[0], catalog)
				}
			}

			protected := v1.Group("")
			if cfg.Auth.Enabled {
				protected.Use(middleware.Authenticate(auth))
				handler.RegisterAuth(protected, auth)
				if len(services) > 0 && services[0] != nil {
					registerMCP(protected, cfg, services[0], catalog)
				}
			} else {
				protected.Use(middleware.ControlKey(cfg.Control.APIKey))
			}
			if len(services) > 0 && services[0] != nil {
				cluster := handler.NewClusterHandler(services[0], time.Duration(cfg.Kubernetes.CommandTimeoutSecond)*time.Second)
				if cfg.HTTPEnabled() && !cfg.MCP.DiagnosticsOnly {
					registerClusterRoutes(protected, cluster)
					handler.RegisterOperations(protected, services[0], time.Duration(cfg.Kubernetes.CommandTimeoutSecond)*time.Second)
					if cfg.Shell.Enabled {
						handler.RegisterTerminals(protected, services[0])
					}
				}
				if cfg.HTTPEnabled() {
					handler.RegisterPodDiagnostics(protected, services[0], cfg.PodExec.Enabled)
				}
				if cfg.FilesEnabled() {
					handler.RegisterPodFileDownload(protected, services[0])
					handler.RegisterPodFileUpload(protected, services[0])
				}
				if cfg.HTTPEnabled() && !cfg.MCP.DiagnosticsOnly {
					runtimeID := uuid.NewString()
					protected.GET("/operator/capabilities", func(c *gin.Context) {
						if !cfg.Auth.Enabled && cfg.Control.APIKey == "" {
							response.AbortWithHTTPStatus(c, 401)
							return
						}
						response.Success(c, gin.H{"clusterId": cfg.ClusterID(), "runtimeId": runtimeID, "version": cfg.App.Version, "apiVersion": "v1", "http": true, "mcp": cfg.MCP.Enabled, "kubernetesWrites": cfg.Kubernetes.WriteEnabled, "mcpWrites": cfg.MCP.Enabled && cfg.MCP.WriteEnabled, "podExec": cfg.PodExec.Enabled, "terminal": cfg.Shell.Enabled && cfg.PodExec.Enabled, "files": cfg.FilesEnabled(), "catalog": catalog != nil})
					})
				}
			}

			if cfg.HTTPEnabled() && catalog != nil && !cfg.MCP.DiagnosticsOnly {
				handler.RegisterCatalog(protected, catalog, time.Duration(cfg.Kubernetes.CommandTimeoutSecond)*time.Second)
			}
		}
	}

	return r, nil
}

func registerMCP(pub *gin.RouterGroup, cfg *config.Config, service *application.ClusterService, catalog *catalogapp.Service) {
	if cfg.MCP.Enabled {
		mcpHandler := mcpiface.NewHandler(service, cfg.App.Version, mcpiface.Options{ShellEnabled: cfg.Shell.Enabled, DiagnosticsOnly: cfg.MCP.DiagnosticsOnly, PodExecEnabled: cfg.PodExec.Enabled, Catalog: catalog, WriteEnabled: cfg.MCP.WriteEnabled, WriteTimeout: time.Duration(cfg.Kubernetes.CommandTimeoutSecond) * time.Second})
		pub.Any("/mcp", func(c *gin.Context) {
			ctx := context.WithValue(c.Request.Context(), logger.TraceIDKey, c.GetString(string(logger.TraceIDKey)))
			mcpHandler.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
		})
	}
}

func registerClusterRoutes(v1 *gin.RouterGroup, clusterHandler *handler.ClusterHandler) {
	cluster := v1.Group("/cluster")
	{
		cluster.GET("/status", clusterHandler.Status)

		namespaces := cluster.Group("/namespaces")
		{
			namespaces.GET("", clusterHandler.Namespaces)
			namespace := namespaces.Group("/:namespace")
			{
				namespace.GET("/resources", clusterHandler.Resources)
				namespace.GET("/gateway-resources", clusterHandler.GatewayResources)
				namespace.POST("/metadata", clusterHandler.UpdateNamespaceMetadata)
			}
		}

		storageClasses := cluster.Group("/storage-classes")
		{
			storageClasses.GET("", clusterHandler.StorageClasses)
		}

		kubectl := cluster.Group("/kubectl")
		{
			kubectl.POST("/apply", clusterHandler.Apply)
		}

		helm := cluster.Group("/helm")
		{
			helm.POST("/upgrade", clusterHandler.HelmUpgrade)
		}
	}
}
