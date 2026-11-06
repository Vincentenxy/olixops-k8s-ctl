package domain

import (
	"path"
	"slices"
	"strings"
)

// DiagnosticGrant is an administrator-defined capability, never supplied by an MCP caller.
// Exact targets keep diagnostic access independent of cluster-admin credentials.
type DiagnosticGrant struct {
	UserID    string   `mapstructure:"user-id"`
	Namespace string   `mapstructure:"namespace"`
	Pod       string   `mapstructure:"pod"`
	Container string   `mapstructure:"container"`
	PodUID    string   `mapstructure:"pod-uid"`
	Actions   []string `mapstructure:"actions"`
	Files     []string `mapstructure:"files"`
}

func ValidDiagnosticFilePath(file string) bool {
	if !path.IsAbs(file) || file == "/" || path.Clean(file) != file || len(file) > 512 {
		return false
	}
	for _, root := range []string{"/proc", "/sys", "/dev", "/var/run/secrets", "/run/secrets"} {
		if file == root || strings.HasPrefix(file, root+"/") {
			return false
		}
	}
	return true
}
func ValidDiagnosticAction(action string) bool {
	return slices.Contains([]string{"upload_file", "download_file", "shell", "logs", "redis_ping", "nginx_check", "disk_usage", "process_list", "environment", "read_file", "process_status", "memory_usage"}, action)
}
