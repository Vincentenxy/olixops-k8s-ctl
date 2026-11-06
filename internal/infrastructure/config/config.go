package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/util/validation"
	"olixops-k8s-ctl/internal/domain"
	authinfra "olixops-k8s-ctl/internal/infrastructure/auth"
)

type Config struct {
	HTTP       HTTPConfig       `mapstructure:"http"`
	Cluster    ClusterConfig    `mapstructure:"cluster"`
	Shell      ShellConfig      `mapstructure:"shell"`
	PodExec    PodExecConfig    `mapstructure:"pod-exec"`
	Auth       authinfra.Config `mapstructure:"auth"`
	Server     ServerConfig     `mapstructure:"server"`
	App        AppConfig        `mapstructure:"app"`
	Kubernetes KubernetesConfig `mapstructure:"kubernetes"`
	MCP        MCPConfig        `mapstructure:"mcp"`
	Control    ControlConfig    `mapstructure:"control"`
	Catalog    CatalogConfig    `mapstructure:"catalog"`
}

// A nil Enabled preserves the historic HTTP-on behavior for programmatic configs.
type HTTPConfig struct {
	Enabled *bool `mapstructure:"enabled"`
}
type ClusterConfig struct {
	ID string `mapstructure:"id"`
}

func (c *Config) HTTPEnabled() bool { return c.HTTP.Enabled == nil || *c.HTTP.Enabled }
func (c *Config) ClusterID() string {
	if c.Cluster.ID != "" {
		return c.Cluster.ID
	}
	if c.Catalog.Cluster != "" {
		return c.Catalog.Cluster
	}
	return "configured"
}
func (c *Config) FilesEnabled() bool {
	return !c.MCP.DiagnosticsOnly && c.Shell.Enabled && c.PodExec.Enabled && (c.HTTPEnabled() || c.MCP.Enabled && c.MCP.WriteEnabled)
}

type CatalogConfig struct {
	DefaultOwner        string `mapstructure:"default-owner"`
	DefaultStorageClass string `mapstructure:"default-storage-class"`
	SandboxRuntimeClass string `mapstructure:"sandbox-runtime-class"`
	Enabled             bool   `mapstructure:"enabled"`
	DatabasePath        string `mapstructure:"database-path"`
	GatewayID           string `mapstructure:"gateway-id"`
	Cluster             string `mapstructure:"cluster"`
	GatewayNamespace    string `mapstructure:"gateway-namespace"`
	GatewayService      string `mapstructure:"gateway-service"`
	GatewayName         string `mapstructure:"gateway-name"`
	PortStart           int    `mapstructure:"port-start"`
	PortEnd             int    `mapstructure:"port-end"`
}

type ShellConfig struct {
	Terminal struct {
		IdleSeconds     int `mapstructure:"idle-seconds"`
		LifetimeSeconds int `mapstructure:"lifetime-seconds"`
		MaxSessions     int `mapstructure:"max-sessions"`
		MaxPerUser      int `mapstructure:"max-per-user"`
	} `mapstructure:"terminal"`
	Enabled     bool `mapstructure:"enabled"`
	Interceptor struct {
		Enabled  bool   `mapstructure:"enabled"`
		Provider string `mapstructure:"provider"`
	} `mapstructure:"interceptor"`
}

type PodExecConfig struct {
	Mode              string                   `mapstructure:"mode"`
	Binary            string                   `mapstructure:"binary"`
	Grants            []domain.DiagnosticGrant `mapstructure:"grants"`
	Enabled           bool                     `mapstructure:"enabled"`
	Endpoint          string                   `mapstructure:"endpoint"`
	AllowedNamespaces []string                 `mapstructure:"allowed-namespaces"`
}

func (c *Config) ValidatePodExec() error {
	if c.Shell.Enabled && (!c.PodExec.Enabled || c.MCP.DiagnosticsOnly) {
		return fmt.Errorf("shell requires pod-exec enabled and diagnostics-only=false")
	}
	if c.Shell.Interceptor.Provider != "" && c.Shell.Interceptor.Provider != "allow-all" {
		return fmt.Errorf("unsupported shell interceptor provider")
	}
	if !c.PodExec.Enabled && !c.MCP.DiagnosticsOnly {
		return nil
	}
	if c.MCP.DiagnosticsOnly && (!c.Auth.Enabled || !c.MCP.Enabled || len(c.PodExec.Grants) == 0) {
		return fmt.Errorf("diagnostics-only requires authentication, MCP and explicit user grants")
	}
	for _, g := range c.PodExec.Grants {
		if strings.TrimSpace(g.UserID) == "" || len(validation.IsDNS1123Label(g.Namespace)) > 0 || len(validation.IsDNS1123Subdomain(g.Pod)) > 0 || len(validation.IsDNS1123Label(g.Container)) > 0 || len(g.Actions) == 0 || (c.MCP.DiagnosticsOnly && g.PodUID == "") {
			return fmt.Errorf("diagnostic grants require an exact user, namespace, pod, container, actions and a Pod UID in diagnostics-only mode")
		}
		for _, action := range g.Actions {
			if !domain.ValidDiagnosticAction(action) {
				return fmt.Errorf("invalid diagnostic grant action")
			}
		}
		if slices.Contains(g.Actions, "read_file") && len(g.Files) == 0 {
			return fmt.Errorf("read_file grant requires explicit files")
		}
		for _, file := range g.Files {
			if !domain.ValidDiagnosticFilePath(file) {
				return fmt.Errorf("invalid or reserved diagnostic grant file")
			}
		}

	}
	if !c.PodExec.Enabled {
		return nil
	}
	if c.MCP.DiagnosticsOnly && !c.MCP.WriteEnabled {
		return fmt.Errorf("diagnostics-only pod-exec requires MCP writes")
	}
	if !c.Auth.Enabled || !c.Kubernetes.WriteEnabled || !(c.HTTPEnabled() || c.MCP.Enabled && c.MCP.WriteEnabled) {
		return fmt.Errorf("pod-exec requires authentication, Kubernetes writes and an enabled HTTP or writable MCP entry")
	}
	if c.PodExec.Mode != "" && c.PodExec.Mode != "sidecar" && c.PodExec.Mode != "managed" {
		return fmt.Errorf("pod-exec mode must be sidecar or managed")
	}
	if c.PodExec.Mode == "managed" && c.PodExec.Binary == "" {
		return fmt.Errorf("managed MCP binary required")
	}
	if c.PodExec.Mode != "managed" && !c.Kubernetes.InCluster {
		return fmt.Errorf("pod-exec sidecar requires kubernetes.in-cluster to keep checks and execution on the same cluster")
	}
	if len(c.PodExec.AllowedNamespaces) == 0 {
		return fmt.Errorf("pod-exec requires explicit allowed-namespaces")
	}
	for _, ns := range c.PodExec.AllowedNamespaces {
		if len(validation.IsDNS1123Label(ns)) > 0 || ns == "default" || ns == "olixops" || strings.HasPrefix(ns, "kube-") {
			return fmt.Errorf("pod-exec namespace is not a business namespace")
		}
		if len(c.Kubernetes.AllowedNamespaces) > 0 {
			allowed := false
			for _, mainNS := range c.Kubernetes.AllowedNamespaces {
				if ns == mainNS {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("pod-exec namespace must be included in kubernetes.allowed-namespaces")
			}
		}
	}
	return nil
}

type MCPConfig struct {
	DiagnosticsOnly bool `mapstructure:"diagnostics-only"`
	Enabled         bool `mapstructure:"enabled"`
	WriteEnabled    bool `mapstructure:"write-enabled"`
}

type ServerConfig struct {
	PublicURL string `mapstructure:"public-url"`
	Host      string `mapstructure:"host"`
	Port      int    `mapstructure:"port"`
	Mode      string `mapstructure:"mode"`
}

type AppConfig struct {
	Name    string `mapstructure:"name"`
	Version string `mapstructure:"version"`
}

type KubernetesConfig struct {
	InCluster            bool             `mapstructure:"in-cluster"`
	Kubeconfig           KubeconfigConfig `mapstructure:"kubeconfig"`
	WriteEnabled         bool             `mapstructure:"write-enabled"`
	AllowedNamespaces    []string         `mapstructure:"allowed-namespaces"`
	CommandTimeoutSecond int              `mapstructure:"command-timeout-seconds"`
	Kubectl              string           `mapstructure:"kubectl"`
	Helm                 string           `mapstructure:"helm"`
}

type KubeconfigConfig struct {
	Source    string `mapstructure:"source"`
	Path      string `mapstructure:"path"`
	Reference string `mapstructure:"reference"`
	Context   string `mapstructure:"context"`
}

type ControlConfig struct {
	APIKey     string `mapstructure:"api-key"`
	APIKeyFile string `mapstructure:"api-key-file"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(path)
	v.SetEnvKeyReplacer(strings.NewReplacer("SHELL", "K8S_CTL_SHELL", ".", "_", "-", "_"))
	v.AutomaticEnv()
	v.SetDefault("http.enabled", true)
	v.SetDefault("cluster.id", "")
	v.SetDefault("server.public-url", "")
	v.SetDefault("control.api-key-file", "")
	v.SetDefault("shell.terminal.idle-seconds", 300)
	v.SetDefault("shell.terminal.lifetime-seconds", 1800)
	v.SetDefault("shell.terminal.max-sessions", 16)
	v.SetDefault("shell.terminal.max-per-user", 4)
	v.SetDefault("shell.enabled", false)
	v.SetDefault("shell.interceptor.enabled", true)
	v.SetDefault("shell.interceptor.provider", "allow-all")
	v.SetDefault("pod-exec.enabled", false)
	v.SetDefault("pod-exec.mode", "sidecar")
	v.SetDefault("pod-exec.binary", "kubernetes-mcp-server")
	v.SetDefault("pod-exec.grants", []domain.DiagnosticGrant{})
	v.SetDefault("mcp.diagnostics-only", false)
	v.SetDefault("pod-exec.endpoint", "http://127.0.0.1:8082/mcp")
	v.SetDefault("pod-exec.allowed-namespaces", []string{})
	for key, value := range map[string]any{
		"auth.enabled": false, "auth.company.enabled": false, "auth.company.issuer": "", "auth.company.audience": "", "auth.company.key-id": "", "auth.company.public-key": "", "auth.company.public-key-file": "",
		"auth.pat.enabled": false, "auth.pat.issuer": "k8s-ctl", "auth.pat.audience": "k8s-ctl", "auth.pat.key-id": "pat-v1", "auth.pat.private-key-file": "", "auth.pat.public-key-file": "", "auth.pat.database-path": "./data/auth.db",
	} {
		v.SetDefault(key, value)
	}
	v.SetDefault("catalog.enabled", false)
	v.SetDefault("catalog.default-owner", "poc")
	v.SetDefault("catalog.default-storage-class", "local-path")
	v.SetDefault("catalog.sandbox-runtime-class", "")
	v.SetDefault("catalog.database-path", "./data/olixops.db")
	v.SetDefault("catalog.gateway-id", "higress")
	v.SetDefault("catalog.cluster", "configured")
	v.SetDefault("catalog.gateway-namespace", "higress-system")
	v.SetDefault("catalog.gateway-service", "higress-gateway")
	v.SetDefault("catalog.gateway-name", "higress-tcp-gateway")
	v.SetDefault("catalog.port-start", 30000)
	v.SetDefault("catalog.port-end", 30200)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.Cluster.ID != "" {
		if strings.TrimSpace(cfg.Cluster.ID) != cfg.Cluster.ID || len(validation.IsDNS1123Subdomain(cfg.Cluster.ID)) > 0 {
			return nil, fmt.Errorf("cluster.id must be a DNS subdomain")
		}
		if (v.InConfig("catalog.cluster") || os.Getenv("CATALOG_CLUSTER") != "") && cfg.Catalog.Cluster != cfg.Cluster.ID {
			return nil, fmt.Errorf("cluster.id and catalog.cluster must match")
		}
		cfg.Catalog.Cluster = cfg.Cluster.ID
	} else {
		cfg.Cluster.ID = cfg.Catalog.Cluster
	}
	if cfg.Shell.Terminal.IdleSeconds < 1 || cfg.Shell.Terminal.LifetimeSeconds < cfg.Shell.Terminal.IdleSeconds || cfg.Shell.Terminal.LifetimeSeconds > 86400 || cfg.Shell.Terminal.MaxSessions < 1 || cfg.Shell.Terminal.MaxSessions > 128 || cfg.Shell.Terminal.MaxPerUser < 1 || cfg.Shell.Terminal.MaxPerUser > cfg.Shell.Terminal.MaxSessions {
		return nil, fmt.Errorf("invalid shell terminal limits")
	}
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return nil, fmt.Errorf("server.port must be between 1 and 65535")
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.PublicURL != "" {
		u, err := url.Parse(cfg.Server.PublicURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" && (u.Scheme != "http" || !isLoopbackHost(u.Hostname())) {
			return nil, fmt.Errorf("server.public-url requires HTTPS or loopback HTTP, without credentials, query or fragment")
		}
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "debug"
	}
	if !cfg.Auth.Enabled && cfg.Control.APIKeyFile != "" {
		if cfg.Control.APIKey != "" {
			return nil, fmt.Errorf("configure only one of control.api-key and control.api-key-file")
		}
		key, err := os.ReadFile(cfg.Control.APIKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read control API key file failed")
		}
		cfg.Control.APIKey = strings.TrimRight(string(key), "\r\n")
		if strings.TrimSpace(cfg.Control.APIKey) == "" {
			return nil, fmt.Errorf("control API key file is empty")
		}
	}
	if !cfg.Auth.Enabled && !isLoopbackHost(cfg.Server.Host) && cfg.Control.APIKey == "" {
		return nil, fmt.Errorf("control.api-key or control.api-key-file is required when server.host is not loopback")
	}
	if cfg.Kubernetes.CommandTimeoutSecond < 1 {
		cfg.Kubernetes.CommandTimeoutSecond = 120
	}
	if cfg.Kubernetes.Kubectl == "" {
		cfg.Kubernetes.Kubectl = "kubectl"
	}
	if cfg.Kubernetes.Helm == "" {
		cfg.Kubernetes.Helm = "helm"
	}
	if err := cfg.ValidatePodExec(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
