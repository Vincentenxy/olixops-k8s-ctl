package tests

import (
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

// Pin the sidecar exposure contract so adding upstream tools cannot silently bypass k8s-ctl.
func TestPrivateSidecarConfiguration(t *testing.T) {
	data, err := os.ReadFile("../deployments/components/kubernetes-mcp/config.toml")
	require.NoError(t, err)
	var cfg struct {
		Bind         string   `toml:"bind_address"`
		Port         string   `toml:"port"`
		Stateless    bool     `toml:"stateless"`
		Provider     string   `toml:"cluster_provider_strategy"`
		Tools        []string `toml:"enabled_tools"`
		BackendLimit int      `toml:"max_backend_response_bytes"`
		ResultLimit  int      `toml:"max_result_bytes"`
	}
	require.NoError(t, toml.Unmarshal(data, &cfg))
	require.Equal(t, "127.0.0.1", cfg.Bind)
	require.Equal(t, "8082", cfg.Port)
	require.True(t, cfg.Stateless)
	require.Equal(t, "in-cluster", cfg.Provider)
	require.Equal(t, []string{"pods_exec"}, cfg.Tools)
	require.Equal(t, 256<<10, cfg.BackendLimit)
	require.Equal(t, 1<<20, cfg.ResultLimit)
}
