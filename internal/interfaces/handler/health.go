package handler

import (
	"github.com/gin-gonic/gin"

	"olixops-k8s-ctl/pkg/response"
)

type HealthHandler struct {
	appName    string
	appVersion string
}

func NewHealthHandler(appName, appVersion string) *HealthHandler {
	return &HealthHandler{appName: appName, appVersion: appVersion}
}

func (h *HealthHandler) Health(c *gin.Context) {
	response.Success(c, gin.H{"status": "ok", "service": h.appName, "version": h.appVersion})
}

// Ready is process-only until a real Kubernetes client is configured.
func (h *HealthHandler) Ready(c *gin.Context) {
	response.Success(c, gin.H{"status": "ready", "service": h.appName, "version": h.appVersion})
}
