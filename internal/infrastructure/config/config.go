package config

import (
	"fmt"
	"net"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	App        AppConfig        `mapstructure:"app"`
	Kubernetes KubernetesConfig `mapstructure:"kubernetes"`
	MCP        MCPConfig        `mapstructure:"mcp"`
	Control    ControlConfig    `mapstructure:"control"`
	Catalog    CatalogConfig    `mapstructure:"catalog"`
}

type CatalogConfig struct {
	Enabled          bool   `mapstructure:"enabled"`
	DatabasePath     string `mapstructure:"database-path"`
	GatewayID        string `mapstructure:"gateway-id"`
	Cluster          string `mapstructure:"cluster"`
	GatewayNamespace string `mapstructure:"gateway-namespace"`
	GatewayService   string `mapstructure:"gateway-service"`
	GatewayName      string `mapstructure:"gateway-name"`
	PortStart        int    `mapstructure:"port-start"`
	PortEnd          int    `mapstructure:"port-end"`
}

type MCPConfig struct {
	Enabled      bool `mapstructure:"enabled"`
	WriteEnabled bool `mapstructure:"write-enabled"`
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
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
	APIKey string `mapstructure:"api-key"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(path)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	v.SetDefault("catalog.enabled", false)
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
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return nil, fmt.Errorf("server.port must be between 1 and 65535")
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "debug"
	}
	if !isLoopbackHost(cfg.Server.Host) && cfg.Control.APIKey == "" {
		return nil, fmt.Errorf("control.api-key is required when server.host is not loopback")
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
	return &cfg, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
