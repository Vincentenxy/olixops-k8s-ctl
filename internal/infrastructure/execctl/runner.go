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
)

type Config struct {
	Kubeconfig     string
	Context        string
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
	args := r.kubeArgs()
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
	args := r.kubeArgs()
	args = append(args, "upgrade", "--install", release, chart, "--namespace", namespace, "--create-namespace")
	if wait {
		args = append(args, "--wait")
	}
	if temporary != "" {
		args = append(args, "--values", temporary)
	}
	return r.run(ctx, r.cfg.Helm, args, nil)
}

func (r *Runner) kubeArgs() []string {
	args := make([]string, 0, 4)
	if r.cfg.Kubeconfig != "" {
		args = append(args, "--kubeconfig", r.cfg.Kubeconfig)
	}
	if r.cfg.Context != "" {
		args = append(args, "--kube-context", r.cfg.Context)
	}
	return args
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
