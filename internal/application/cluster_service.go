package application

import (
	"context"
	"fmt"
	"strings"

	"olixops-k8s-ctl/internal/domain"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

type ClusterService struct {
	client       *clusterk8s.Client
	runner       *execctl.Runner
	writeEnabled bool
	allowedNS    map[string]struct{}
	authorizer   domain.Authorizer
	cluster      string
}

func NewClusterService(client *clusterk8s.Client, runner *execctl.Runner, cfg config.KubernetesConfig, options ...ServiceOption) *ClusterService {
	allowed := make(map[string]struct{}, len(cfg.AllowedNamespaces))
	for _, namespace := range cfg.AllowedNamespaces {
		if trimmed := strings.TrimSpace(namespace); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	service := &ClusterService{client: client, runner: runner, writeEnabled: cfg.WriteEnabled, allowedNS: allowed, authorizer: domain.DeferredAuthorizer{}, cluster: "configured"}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *ClusterService) Status(ctx context.Context) (clusterk8s.ClusterStatus, error) {
	if err := s.authorize(ctx, "cluster.read", false, nil, nil); err != nil {
		return clusterk8s.ClusterStatus{}, err
	}
	status, err := s.client.Status(ctx)
	if err != nil {
		return status, err
	}
	if len(s.allowedNS) > 0 {
		namespaces, err := s.ListNamespaces(ctx)
		if err != nil {
			return status, err
		}
		status.Namespaces = len(namespaces)
	}
	return status, nil
}

func (s *ClusterService) ListNamespaces(ctx context.Context) ([]clusterk8s.NamespaceSummary, error) {
	if err := s.authorize(ctx, "namespace.list", false, nil, nil); err != nil {
		return nil, err
	}
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
	if err := s.authorize(ctx, "storageclass.list", false, nil, nil); err != nil {
		return nil, err
	}
	return s.client.ListStorageClasses(ctx)
}

func (s *ClusterService) ListResources(ctx context.Context, namespace string) (clusterk8s.NamespaceResources, error) {
	if err := s.requireNamespace(namespace); err != nil {
		return clusterk8s.NamespaceResources{}, err
	}
	if err := s.authorize(ctx, "resource.list", false, []domain.Target{{Namespace: namespace, Kind: "*"}}, nil); err != nil {
		return clusterk8s.NamespaceResources{}, err
	}
	return s.client.ListResources(ctx, namespace)
}

func (s *ClusterService) ListGatewayResources(ctx context.Context, namespace string) (clusterk8s.GatewaySnapshot, error) {
	if err := s.requireNamespace(namespace); err != nil {
		return clusterk8s.GatewaySnapshot{}, err
	}
	if err := s.authorize(ctx, "gateway.list", false, []domain.Target{{Namespace: namespace, Kind: "Gateway"}, {Kind: "GatewayClass"}}, nil); err != nil {
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
	action := "manifest.apply"
	if dryRun {
		action = "manifest.preview"
	}
	targets := make([]domain.Target, 0, len(resources))
	for _, resource := range resources {
		targets = append(targets, domain.Target{APIVersion: resource.APIVersion, Kind: resource.Kind, Namespace: resource.Namespace, Name: resource.Name})
	}
	if err := s.authorize(ctx, action, true, targets, []any{manifest, serverSide, dryRun}); err != nil {
		return execctl.Result{}, nil, err
	}
	if s.runner == nil {
		return execctl.Result{}, nil, fmt.Errorf("command runner is not configured")
	}
	result, err := s.runner.KubectlApply(ctx, manifest, namespace, serverSide, dryRun)
	return result, resources, err
}

func (s *ClusterService) HelmUpgrade(ctx context.Context, release, chart, namespace, valuesYAML string, wait bool) (execctl.Result, error) {
	return s.UpgradeHelm(ctx, HelmUpgradeInput{Namespace: namespace, Release: release, Chart: chart, ValuesYAML: valuesYAML, Wait: wait})
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

// ValidateManifest performs only local policy and YAML checks; it never contacts Kubernetes.
func (s *ClusterService) ValidateManifest(ctx context.Context, manifest, namespace string) ([]ManifestResource, error) {
	if err := s.requireNamespace(namespace); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, "manifest.validate", false, []domain.Target{{Namespace: namespace, Kind: "*"}}, manifest); err != nil {
		return nil, err
	}
	return ValidateManifest(manifest, namespace, s.allowedNS)
}

func (s *ClusterService) GetRolloutStatus(ctx context.Context, namespace, kind, name string) (clusterk8s.RolloutStatus, error) {
	target, err := s.namedTarget(namespace, kind, name)
	if err != nil {
		return clusterk8s.RolloutStatus{}, err
	}
	if err := workloadKind(kind, false); err != nil {
		return clusterk8s.RolloutStatus{}, err
	}
	if err := s.authorize(ctx, "workload.status", false, []domain.Target{target}, nil); err != nil {
		return clusterk8s.RolloutStatus{}, err
	}
	return s.client.GetRolloutStatus(ctx, namespace, kind, name)
}
