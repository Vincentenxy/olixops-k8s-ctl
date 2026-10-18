package execctl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	if cfg.TimeoutSeconds < 1 {
		cfg.TimeoutSeconds = 120
	}
	if cfg.Kubectl == "" {
		cfg.Kubectl = "kubectl"
	}
	if cfg.Helm == "" {
		cfg.Helm = "helm"
	}
	return &Runner{cfg: cfg}
}

func (r *Runner) KubectlApply(ctx context.Context, manifest, namespace string, serverSide, dryRun bool) (Result, error) {
	kubeconfigPath, contextName, cleanup, err := r.prepareKubeconfig(ctx)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	args := r.kubeArgs(kubeconfigPath, contextName, false)
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
	_, err = r.runRaw(ctx, r.cfg.Kubectl, args, []byte(manifest))
	if err != nil {
		return Result{}, err
	}
	return Result{Output: "kubectl apply completed"}, nil
}

func (r *Runner) kubeArgs(kubeconfigPath, contextName string, helm bool) []string {
	args := []string{"--kubeconfig", kubeconfigPath}
	if contextName != "" {
		flag := "--context"
		if helm {
			flag = "--kube-context"
		}
		args = append(args, flag, contextName)
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

// cappedBuffer bounds memory during execution, not just after capture.
type cappedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.overflow = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func (r *Runner) runRaw(parent context.Context, binary string, args []string, stdin []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, time.Duration(r.cfg.TimeoutSeconds)*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.WaitDelay = time.Second
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output := &cappedBuffer{limit: 8 << 20}
	command.Stdout = output
	// stderr can contain credentials, rendered values or server addresses.
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("command timed out or was canceled")
		}
		return nil, fmt.Errorf("external command failed")
	}
	if output.overflow {
		return nil, fmt.Errorf("command output exceeds 8 MiB")
	}
	return output.Bytes(), nil
}
