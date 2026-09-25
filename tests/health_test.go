package tests

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/interfaces/router"
	"olixops-k8s-ctl/pkg/logger"
)

func TestHealthEndpoint(t *testing.T) {
	logger.Init("test")
	gin.SetMode(gin.TestMode)
	r, err := router.New(&config.Config{
		Server: config.ServerConfig{Mode: "test", Port: 8091},
		App:    config.AppConfig{Name: "olixops-k8s-ctl", Version: "test"},
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/v1/pub/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, 200, rec.Code)
	require.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"code":0`)
	require.Contains(t, rec.Body.String(), `"status":"ok"`)
}
