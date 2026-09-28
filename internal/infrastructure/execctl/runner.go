package execctl

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
)

type Config struct {
	Provider       kubeconfig.Provider
	Reference      kubeconfig.Reference
	Kubectl        string
	Helm           string
	TimeoutSeconds int
}

type Result struct {
	Command string `json:"-"`
	Output  string `json:"output"`
}

type Runner struct {
	cfg Config
}

func New(cfg Config) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) KubectlApply(ctx context.Context, manifest, namespace string, serverSide, dryRun bool) (Result, error) {
	kubeconfigPath, contextName, cleanup, err := r.prepareKubeconfig(ctx)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	args := r.kubeArgs(kubeconfigPath, contextName)
	args = append(args, "apply", "-f", "-")
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	if serverSide {
		args = append(args, "--server-side", "--field-manager", "olixops-k8s-ctl")
	}
	if dryRun {
		args = append(args, "--dry-run=server")
	}
	return r.run(ctx, r.cfg.Kubectl, args, []byte(manifest))
}

func (r *Runner) HelmUpgrade(ctx context.Context, release, chart, namespace, valuesYAML string, wait bool) (Result, error) {
	kubeconfigPath, contextName, cleanupKubeconfig, err := r.prepareKubeconfig(ctx)
	if err != nil {
		return Result{}, err
	}
	defer cleanupKubeconfig()
	temporary := ""
	if strings.TrimSpace(valuesYAML) != "" {
		directory, err := os.MkdirTemp("", "olixops-k8s-ctl-helm-")
		if err != nil {
			return Result{}, fmt.Errorf("create helm values directory: %w", err)
		}
		defer os.RemoveAll(directory)
		temporary = filepath.Join(directory, "values.yaml")
		if err := os.WriteFile(temporary, []byte(valuesYAML), 0o600); err != nil {
			return Result{}, fmt.Errorf("write helm values: %w", err)
		}
	}
	args := r.kubeArgs(kubeconfigPath, contextName)
	args = append(args, "upgrade", "--install", release, chart, "--namespace", namespace, "--create-namespace")
	if wait {
		args = append(args, "--wait")
	}
	if temporary != "" {
		args = append(args, "--values", temporary)
	}
	return r.run(ctx, r.cfg.Helm, args, nil)
}

func (r *Runner) kubeArgs(kubeconfigPath, contextName string) []string {
	args := make([]string, 0, 4)
	if kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}
	if contextName != "" {
		args = append(args, "--kube-context", contextName)
	}
	return args
}

func (r *Runner) prepareKubeconfig(ctx context.Context) (string, string, func(), error) {
	if r.cfg.Provider == nil {
		return "", "", func() {}, fmt.Errorf("kubeconfig provider is not configured")
	}
	document, err := r.cfg.Provider.Load(ctx, r.cfg.Reference)
	if err != nil {
		return "", "", func() {}, err
	}
	temporary, err := os.CreateTemp("", "olixops-k8s-ctl-kubeconfig-*.yaml")
	if err != nil {
		return "", "", func() {}, fmt.Errorf("create temporary kubeconfig: %w", err)
	}
	path := temporary.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		cleanup()
		return "", "", func() {}, fmt.Errorf("protect temporary kubeconfig: %w", err)
	}
	if _, err := temporary.Write(document.Bytes); err != nil {
		_ = temporary.Close()
		cleanup()
		return "", "", func() {}, fmt.Errorf("write temporary kubeconfig: %w", err)
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return "", "", func() {}, fmt.Errorf("close temporary kubeconfig: %w", err)
	}
	return path, r.cfg.Reference.Context, cleanup, nil
}

func (r *Runner) run(parent context.Context, binary string, args []string, stdin []byte) (Result, error) {
	timeout := time.Duration(r.cfg.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	result := Result{Command: binary + " " + strings.Join(redactArgs(args), " "), Output: truncate(string(output), 16*1024)}
	if ctx.Err() != nil {
		return result, fmt.Errorf("command timeout: %w", ctx.Err())
	}
	if err != nil {
		return result, fmt.Errorf("command failed: %w", err)
	}
	return result, nil
}

func redactArgs(args []string) []string {
	result := append([]string(nil), args...)
	for index := range result {
		if result[index] == "--values" && index+1 < len(result) {
			result[index+1] = "<temporary-values>"
		}
	}
	return result
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "\n<output truncated>"
}
