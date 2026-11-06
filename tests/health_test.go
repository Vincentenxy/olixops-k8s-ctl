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

func TestUnknownRoutesUseJSONEnvelope(t *testing.T) {
	r, err := router.New(&config.Config{Server: config.ServerConfig{Mode: "test"}})
	require.NoError(t, err)
	for _, path := range []string{"/api/v1/not-found", "/api/v1/cluster/pods/exec", "/api/v1/pub/mcp"} {
		req := httptest.NewRequest("POST", path, nil)
		req.Header.Set("X-Request-Id", "missing-route-test")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, 404, rec.Code)
		require.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
		require.JSONEq(t, `{"code":404,"msg":"Not Found","data":{"reason":"not_found"}}`, rec.Body.String())
		require.Equal(t, "missing-route-test", rec.Header().Get("X-Request-Id"))
	}
}
