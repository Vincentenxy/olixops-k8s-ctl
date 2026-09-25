package router

import (
	"github.com/gin-gonic/gin"

	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/interfaces/handler"
	"olixops-k8s-ctl/internal/interfaces/middleware"
)

func New(cfg *config.Config) (*gin.Engine, error) {
	gin.SetMode(cfg.Server.Mode)
	r := gin.New()
	r.Use(middleware.RequestID(), middleware.GinLogger(), gin.Recovery())

	health := handler.NewHealthHandler(cfg.App.Name, cfg.App.Version)
	v1 := r.Group("/api/v1")
	pub := v1.Group("/pub")
	pub.GET("/health", health.Health)
	pub.GET("/ready", health.Ready)

	return r, nil
}
