package k8smcp

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// NewManaged reuses the upstream server over private stdio, with the same frozen
// Provider used by client-go. No client identity or kubeconfig is sent to developers.
func NewManaged(binary string, provider *kubeconfig.SnapshotProvider) (*Client, error) {
	if provider == nil {
		return nil, fmt.Errorf("managed MCP requires a frozen cluster Provider")
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("managed MCP binary unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, resolved, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "v0.1.2" {
		return nil, fmt.Errorf("managed MCP requires upstream v0.1.2")
	}
	return &Client{managedBinary: resolved, provider: provider, slots: make(chan struct{}, 4)}, nil
}
func (c *Client) managedTransport(ctx context.Context) (mcp.Transport, func(), error) {
	document, err := c.provider.Load(ctx, kubeconfig.Reference{})
	if err != nil {
		return nil, nil, fmt.Errorf("load managed MCP credentials failed")
	}
	dir, err := os.MkdirTemp("", "k8sctl-managed-mcp-")
	if err != nil {
		return nil, nil, fmt.Errorf("create managed MCP workspace failed")
	}
	cleanup := func() { os.RemoveAll(dir) }
	credential := filepath.Join(dir, "cluster.yaml")
	if err = os.WriteFile(credential, document.Bytes, 0600); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("prepare managed MCP credentials failed")
	}
	config := `port = ""
cluster_provider_strategy = "kubeconfig"
toolsets = ["core"]
enabled_tools = ["pods_exec"]
read_only = false
max_backend_response_bytes = 262144
max_result_bytes = 1048576
max_in_flight = 1
log_level = 0
log_file = "stderr"
kubeconfig = ` + strconv.Quote(credential) + "\n"
	configFile := filepath.Join(dir, "config.toml")
	if err = os.WriteFile(configFile, []byte(config), 0600); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("prepare managed MCP configuration failed")
	}
	cmd := exec.CommandContext(ctx, c.managedBinary, "--config", configFile)
	// Do not inherit client credentials or another MCP config/drop-in from the parent environment.
	for _, pair := range os.Environ() {
		key, _, _ := strings.Cut(pair, "=")
		if key == "PATH" || key == "HOME" || key == "TMPDIR" || key == "SSL_CERT_FILE" || key == "SSL_CERT_DIR" {
			cmd.Env = append(cmd.Env, pair)
		}
	}
	cmd.Dir = dir
	return &mcp.CommandTransport{Command: cmd, TerminateDuration: 100 * time.Millisecond}, cleanup, nil
}
