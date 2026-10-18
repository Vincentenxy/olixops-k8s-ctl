package execctl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type HelmRelease struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   int    `json:"revision"`
	Status     string `json:"status"`
	Chart      string `json:"chart,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
}
type HelmRevision struct {
	Revision   int    `json:"revision"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
	// Deliberately omit description, values, hooks, NOTES, and manifests.
}
type HelmUpgradeOptions struct {
	Release    string `json:"release"`
	Chart      string `json:"chart"`
	Namespace  string `json:"namespace"`
	Version    string `json:"version,omitempty"`
	ValuesYAML string `json:"valuesYaml,omitempty"`
	Wait       bool   `json:"wait,omitempty"`
}

func (r *Runner) helm(ctx context.Context, args ...string) ([]byte, error) {
	path, contextName, cleanup, err := r.prepareKubeconfig(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return r.runRaw(ctx, r.cfg.Helm, append(r.kubeArgs(path, contextName, true), args...), nil)
}
func (r *Runner) ListHelmReleases(ctx context.Context, namespace string, limit, offset int) ([]HelmRelease, error) {
	data, err := r.helm(ctx, "list", "--namespace", namespace, "--all", "--max", strconv.Itoa(limit), "--offset", strconv.Itoa(offset), "--output", "json")
	if err != nil {
		return nil, err
	}
	var releases []struct {
		Name       string `json:"name"`
		Namespace  string `json:"namespace"`
		Revision   string `json:"revision"`
		Status     string `json:"status"`
		Chart      string `json:"chart"`
		AppVersion string `json:"app_version"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return nil, fmt.Errorf("decode Helm release list")
	}
	result := make([]HelmRelease, 0, len(releases))
	for _, release := range releases {
		revision, err := strconv.Atoi(release.Revision)
		if err != nil {
			return nil, fmt.Errorf("decode Helm revision")
		}
		result = append(result, HelmRelease{Name: release.Name, Namespace: release.Namespace, Revision: revision, Status: release.Status, Chart: release.Chart, AppVersion: release.AppVersion})
	}
	return result, nil
}
func (r *Runner) GetHelmStatus(ctx context.Context, namespace, release string) (HelmRelease, error) {
	data, err := r.helm(ctx, "status", release, "--namespace", namespace, "--output", "json")
	if err != nil {
		return HelmRelease{}, err
	}
	return decodeHelmStatus(data)
}
func decodeHelmStatus(data []byte) (HelmRelease, error) {
	var release struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Version   int    `json:"version"`
		Info      struct {
			Status string `json:"status"`
		} `json:"info"`
		Chart struct {
			Metadata struct {
				Name       string `json:"name"`
				Version    string `json:"version"`
				AppVersion string `json:"appVersion"`
			} `json:"metadata"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return HelmRelease{}, fmt.Errorf("decode Helm status")
	}
	return HelmRelease{Name: release.Name, Namespace: release.Namespace, Revision: release.Version, Status: release.Info.Status, Chart: release.Chart.Metadata.Name + "-" + release.Chart.Metadata.Version, AppVersion: release.Chart.Metadata.AppVersion}, nil
}
func (r *Runner) GetHelmHistory(ctx context.Context, namespace, release string, limit int) ([]HelmRevision, error) {
	data, err := r.helm(ctx, "history", release, "--namespace", namespace, "--max", strconv.Itoa(limit), "--output", "json")
	if err != nil {
		return nil, err
	}
	result := []HelmRevision{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode Helm history")
	}
	return result, nil
}

// Check the stored release, too: upgrade/rollback/uninstall may remove previously
// installed objects. Hooks are always disabled for mutations in this adapter.
func (r *Runner) checkHelmRevision(ctx context.Context, namespace, release string, revision int, validate func(string) error) error {
	args := []string{"get", "manifest", release, "--namespace", namespace}
	if revision > 0 {
		args = append(args, "--revision", strconv.Itoa(revision))
	}
	data, err := r.helm(ctx, args...)
	if err != nil {
		return err
	}
	return validate(string(data))
}

func (r *Runner) HelmUpgradeChecked(ctx context.Context, options HelmUpgradeOptions, validate func(string) error) (Result, error) {
	// Query a single exact release using a fixed, validated DNS name in the regex.
	existing, err := r.helm(ctx, "list", "--namespace", options.Namespace, "--all", "--filter", "^"+options.Release+"$", "--output", "json")
	if err != nil {
		return Result{}, err
	}
	var releases []json.RawMessage
	if err := json.Unmarshal(existing, &releases); err != nil {
		return Result{}, fmt.Errorf("decode Helm release list")
	}
	if len(releases) > 0 {
		if err := r.checkHelmRevision(ctx, options.Namespace, options.Release, 0, validate); err != nil {
			return Result{}, err
		}
	}
	directory, err := os.MkdirTemp("", "olixops-helm-chart-")
	if err != nil {
		return Result{}, fmt.Errorf("create chart directory")
	}
	defer os.RemoveAll(directory)
	args := []string{"pull", options.Chart, "--destination", directory}
	if options.Version != "" {
		args = append(args, "--version", options.Version)
	}
	if _, err := r.helm(ctx, args...); err != nil {
		return Result{}, err
	}
	charts, err := filepath.Glob(filepath.Join(directory, "*.tgz"))
	if err != nil || len(charts) != 1 {
		return Result{}, fmt.Errorf("expected a single downloaded chart archive")
	}
	// Render and install the exact same downloaded artifact, including for an unpinned version.
	valuesPath := ""
	if strings.TrimSpace(options.ValuesYAML) != "" {
		valuesPath = filepath.Join(directory, "values.yaml")
		if err := os.WriteFile(valuesPath, []byte(options.ValuesYAML), 0o600); err != nil {
			return Result{}, fmt.Errorf("write Helm values")
		}
	}
	render := []string{"template", options.Release, charts[0], "--namespace", options.Namespace, "--include-crds", "--no-hooks"}
	if len(releases) > 0 {
		render = append(render, "--is-upgrade")
	}
	if valuesPath != "" {
		render = append(render, "--values", valuesPath)
	}
	data, err := r.helm(ctx, render...)
	if err != nil {
		return Result{}, err
	}
	if err := validate(string(data)); err != nil {
		return Result{}, err
	}
	// Mutation output is discarded: Helm prints potentially sensitive NOTES.
	args = []string{"upgrade", "--install", options.Release, charts[0], "--namespace", options.Namespace, "--no-hooks", "--reset-values", "--timeout", strconv.Itoa(r.cfg.TimeoutSeconds) + "s"}
	if options.Wait {
		args = append(args, "--wait")
	}
	if valuesPath != "" {
		args = append(args, "--values", valuesPath)
	}
	if _, err := r.helm(ctx, args...); err != nil {
		return Result{}, err
	}
	return Result{Output: "Helm upgrade/install completed"}, nil
}
func (r *Runner) HelmRollbackChecked(ctx context.Context, namespace, release string, revision int, wait bool, validate func(string) error) (Result, error) {
	if err := r.checkHelmRevision(ctx, namespace, release, 0, validate); err != nil {
		return Result{}, err
	}
	if err := r.checkHelmRevision(ctx, namespace, release, revision, validate); err != nil {
		return Result{}, err
	}
	args := []string{"rollback", release, strconv.Itoa(revision), "--namespace", namespace, "--no-hooks", "--timeout", strconv.Itoa(r.cfg.TimeoutSeconds) + "s"}
	if wait {
		args = append(args, "--wait")
	}
	if _, err := r.helm(ctx, args...); err != nil {
		return Result{}, err
	}
	return Result{Output: "Helm rollback completed"}, nil
}
func (r *Runner) HelmUninstallChecked(ctx context.Context, namespace, release string, wait bool, validate func(string) error) (Result, error) {
	if err := r.checkHelmRevision(ctx, namespace, release, 0, validate); err != nil {
		return Result{}, err
	}
	args := []string{"uninstall", release, "--namespace", namespace, "--no-hooks", "--timeout", strconv.Itoa(r.cfg.TimeoutSeconds) + "s"}
	if wait {
		args = append(args, "--wait")
	}
	if _, err := r.helm(ctx, args...); err != nil {
		return Result{}, err
	}
	return Result{Output: "Helm uninstall completed"}, nil
}

func (r *Runner) RollbackWorkload(ctx context.Context, namespace, kind, name string, revision int) (Result, error) {
	path, contextName, cleanup, err := r.prepareKubeconfig(ctx)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	args := append(r.kubeArgs(path, contextName, false), "rollout", "undo", strings.ToLower(kind)+"/"+name, "--namespace", namespace, "--to-revision", strconv.Itoa(revision))
	if _, err := r.runRaw(ctx, r.cfg.Kubectl, args, nil); err != nil {
		return Result{}, err
	}
	return Result{Output: "workload rollback requested"}, nil
}
