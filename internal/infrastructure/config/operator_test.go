package config

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestOperatorExecDoesNotRequireMCP(t *testing.T) {
	c := &Config{PodExec: PodExecConfig{Enabled: true, Mode: "managed", Binary: "fixture", AllowedNamespaces: []string{"demo"}}, Kubernetes: KubernetesConfig{WriteEnabled: true}}
	c.Auth.Enabled = true
	c.Shell.Enabled = true
	require.NoError(t, c.ValidatePodExec())
	off := false
	c.HTTP.Enabled = &off
	require.Error(t, c.ValidatePodExec())
	c.MCP.Enabled = true
	c.MCP.WriteEnabled = true
	require.NoError(t, c.ValidatePodExec())
	require.True(t, c.FilesEnabled())
	c.MCP.WriteEnabled = false
	require.Error(t, c.ValidatePodExec())
}
func TestClusterIdentityAndHTTPConfig(t *testing.T) {
	for _, tc := range []struct {
		name, extra, id string
		fail, http      bool
	}{
		{"legacy", "", "configured", false, true},
		{"catalog", "catalog:\n  cluster: poc\n", "poc", false, true},
		{"operator", "cluster:\n  id: dev-b\nhttp:\n  enabled: false\n", "dev-b", false, false},
		{"match", "cluster:\n  id: poc\ncatalog:\n  cluster: poc\n", "poc", false, true},
		{"mismatch", "cluster:\n  id: poc\ncatalog:\n  cluster: other\n", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server:\n  port: 8091\n"+tc.extra), 0600))
			c, e := Load(dir)
			if tc.fail {
				require.Error(t, e)
				return
			}
			require.NoError(t, e)
			require.Equal(t, tc.id, c.ClusterID())
			require.Equal(t, tc.id, c.Catalog.Cluster)
			require.Equal(t, tc.http, c.HTTPEnabled())
		})
	}
}
