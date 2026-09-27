package application

import (
	"context"
	"fmt"
	"strings"

	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

type ClusterService struct {
	client       *clusterk8s.Client
	runner       *execctl.Runner
	writeEnabled bool
	allowedNS    map[string]struct{}
	chartRoots   []string
}

func NewClusterService(client *clusterk8s.Client, runner *execctl.Runner, cfg config.KubernetesConfig) *ClusterService {
	allowed := make(map[string]struct{}, len(cfg.AllowedNamespaces))
	for _, namespace := range cfg.AllowedNamespaces {
		if trimmed := strings.TrimSpace(namespace); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	return &ClusterService{client: client, runner: runner, writeEnabled: cfg.WriteEnabled, allowedNS: allowed}
}

func (s *ClusterService) Status(ctx context.Context) (clusterk8s.ClusterStatus, error) {
	return s.client.Status(ctx)
}

func (s *ClusterService) ListNamespaces(ctx context.Context) ([]clusterk8s.NamespaceSummary, error) {
	items, err := s.client.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	if len(s.allowedNS) == 0 {
		return items, nil
	}
	filtered := make([]clusterk8s.NamespaceSummary, 0, len(items))
	for _, item := range items {
		if s.namespaceAllowed(item.Name) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *ClusterService) ListStorageClasses(ctx context.Context) ([]clusterk8s.StorageClassSummary, error) {
	return s.client.ListStorageClasses(ctx)
}

func (s *ClusterService) ListResources(ctx context.Context, namespace string) (clusterk8s.NamespaceResources, error) {
	if err := s.requireNamespace(namespace); err != nil {
		return clusterk8s.NamespaceResources{}, err
	}
	return s.client.ListResources(ctx, namespace)
}

func (s *ClusterService) ListGatewayResources(ctx context.Context, namespace string) (clusterk8s.GatewaySnapshot, error) {
	if err := s.requireNamespace(namespace); err != nil {
		return clusterk8s.GatewaySnapshot{}, err
	}
	return s.client.ListGatewayResources(ctx, namespace)
}

func (s *ClusterService) ApplyManifest(ctx context.Context, manifest, namespace string, serverSide, dryRun bool) (execctl.Result, []ManifestResource, error) {
	if !s.writeEnabled {
		return execctl.Result{}, nil, fmt.Errorf("kubernetes write operations are disabled")
	}
	if err := s.requireNamespace(namespace); err != nil {
		return execctl.Result{}, nil, err
	}
	resources, err := ValidateManifest(manifest, namespace, s.allowedNS)
	if err != nil {
		return execctl.Result{}, nil, err
	}
	result, err := s.runner.KubectlApply(ctx, manifest, namespace, serverSide, dryRun)
	return result, resources, err
}

func (s *ClusterService) HelmUpgrade(ctx context.Context, release, chart, namespace, valuesYAML string, wait bool) (execctl.Result, error) {
	if !s.writeEnabled {
		return execctl.Result{}, fmt.Errorf("kubernetes write operations are disabled")
	}
	if err := s.requireNamespace(namespace); err != nil {
		return execctl.Result{}, err
	}
	if err := validateName(release, "release"); err != nil {
		return execctl.Result{}, err
	}
	if strings.TrimSpace(chart) == "" {
		return execctl.Result{}, fmt.Errorf("chart is required")
	}
	if strings.ContainsAny(chart, "\r\n") {
		return execctl.Result{}, fmt.Errorf("chart contains invalid characters")
	}
	return s.runner.HelmUpgrade(ctx, release, chart, namespace, valuesYAML, wait)
}

func (s *ClusterService) namespaceAllowed(namespace string) bool {
	if len(s.allowedNS) == 0 {
		return true
	}
	_, ok := s.allowedNS[namespace]
	return ok
}

func (s *ClusterService) requireNamespace(namespace string) error {
	if err := validateName(namespace, "namespace"); err != nil {
		return err
	}
	if !s.namespaceAllowed(namespace) {
		return fmt.Errorf("namespace %q is not allowed", namespace)
	}
	return nil
}

func validateName(value, field string) error {
	if value == "" || len(value) > 63 || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return fmt.Errorf("%s must be a DNS label", field)
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return fmt.Errorf("%s must be a lowercase DNS label", field)
		}
	}
	return nil
}
