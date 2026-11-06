package config

import (
	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/domain"
	"testing"
)

func TestExecConfigurationGates(t *testing.T) {
	good := func() *Config {
		off := false
		return &Config{HTTP: HTTPConfig{Enabled: &off}, PodExec: PodExecConfig{Enabled: true, AllowedNamespaces: []string{"demo"}}, MCP: MCPConfig{Enabled: true, WriteEnabled: true}, Kubernetes: KubernetesConfig{WriteEnabled: true, InCluster: true}}
	}
	c := good()
	c.Auth.Enabled = true
	require.NoError(t, c.ValidatePodExec())
	for _, change := range []func(*Config){func(c *Config) { c.Kubernetes.InCluster = false }, func(c *Config) { c.Kubernetes.AllowedNamespaces = []string{"other"} }, func(c *Config) { c.Auth.Enabled = false }, func(c *Config) { c.MCP.Enabled = false }, func(c *Config) { c.MCP.WriteEnabled = false }, func(c *Config) { c.Kubernetes.WriteEnabled = false }, func(c *Config) { c.PodExec.AllowedNamespaces = nil }, func(c *Config) { c.PodExec.AllowedNamespaces = []string{"kube-system"} }, func(c *Config) { c.PodExec.AllowedNamespaces = []string{"default"} }} {
		c := good()
		c.Auth.Enabled = true
		change(c)
		require.Error(t, c.ValidatePodExec())
	}
	require.NoError(t, (&Config{}).ValidatePodExec())
}

func TestDiagnosticOnlyRequiresPinnedExplicitGrants(t *testing.T) {
	cfg := &Config{MCP: MCPConfig{Enabled: true, DiagnosticsOnly: true}}
	cfg.Auth.Enabled = true
	require.Error(t, cfg.ValidatePodExec())
	cfg.PodExec.Grants = []domain.DiagnosticGrant{{UserID: "alice", Namespace: "demo", Pod: "app", Container: "app", Actions: []string{"logs"}}}
	require.Error(t, cfg.ValidatePodExec())
	cfg.PodExec.Grants[0].PodUID = "uid-1"
	require.NoError(t, cfg.ValidatePodExec())
	cfg.PodExec.Grants[0].Actions = []string{"read_file"}
	require.Error(t, cfg.ValidatePodExec())
	cfg.PodExec.Grants[0].Files = []string{"/var/run/secrets/kubernetes.io/serviceaccount/token"}
	require.Error(t, cfg.ValidatePodExec())
	cfg.PodExec.Grants[0].Files = []string{"/app/config.yaml"}
	require.NoError(t, cfg.ValidatePodExec())
}

func TestShellRequiresMainAuthenticatedExecution(t *testing.T) {
	c := &Config{}
	c.Shell.Enabled = true
	require.Error(t, c.ValidatePodExec())
	c.PodExec = PodExecConfig{Enabled: true, Mode: "managed", Binary: "upstream", AllowedNamespaces: []string{"demo"}}
	c.Auth.Enabled = true
	c.MCP = MCPConfig{Enabled: true, WriteEnabled: true}
	c.Kubernetes.WriteEnabled = true
	c.Shell.Interceptor.Enabled = true
	c.Shell.Interceptor.Provider = "allow-all"
	require.NoError(t, c.ValidatePodExec())
	c.MCP.DiagnosticsOnly = true
	require.Error(t, c.ValidatePodExec())
	c.MCP.DiagnosticsOnly = false
	c.Shell.Interceptor.Provider = "unknown"
	require.Error(t, c.ValidatePodExec())
}
