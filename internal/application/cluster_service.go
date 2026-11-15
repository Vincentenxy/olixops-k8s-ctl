package application

import (
	"context"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"olixops-k8s-ctl/internal/domain/fault"
	"strings"

	"olixops-k8s-ctl/internal/domain"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

type ClusterService struct {
	uploads                 ticketStore
	downloads               ticketStore
	transferBaseURL         string
	terminals               *sessions.Manager
	streamBackend           domain.PodStreamBackend
	shellEnabled            bool
	shellIntercept          bool
	shellInterceptor        interceptor.Interceptor
	diagnosticGrantsEnabled bool
	diagnosticGrants        []domain.DiagnosticGrant
	podExecutor             domain.PodExecutor
	client                  *clusterk8s.Client
	runner                  *execctl.Runner
	writeEnabled            bool
	authorizer              domain.Authorizer
	cluster                 string
}

func NewClusterService(client *clusterk8s.Client, runner *execctl.Runner, cfg config.KubernetesConfig, options ...ServiceOption) *ClusterService {
	service := &ClusterService{client: client, runner: runner, writeEnabled: cfg.WriteEnabled, authorizer: domain.DeferredAuthorizer{}, cluster: "configured"}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *ClusterService) Status(ctx context.Context) (clusterk8s.ClusterStatus, error) {
	if err := s.authorize(ctx, "cluster.read", false, nil, nil); err != nil {
		return clusterk8s.ClusterStatus{}, err
	}
	return s.client.Status(ctx)
}

func (s *ClusterService) ListNamespaces(ctx context.Context) ([]clusterk8s.NamespaceSummary, error) {
	if err := s.authorize(ctx, "namespace.list", false, nil, nil); err != nil {
		return nil, err
	}
	return s.client.ListNamespaces(ctx)
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
		return execctl.Result{}, nil, fault.Disabled("kubernetes write operations are disabled")
	}
	if err := s.requireNamespace(namespace); err != nil {
		return execctl.Result{}, nil, err
	}
	resources, err := ValidateManifest(manifest, namespace)
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
		return execctl.Result{}, nil, fault.Disabled("command runner is not configured")
	}
	result, err := s.runner.KubectlApply(ctx, manifest, namespace, serverSide, dryRun)
	return result, resources, err
}

func (s *ClusterService) HelmUpgrade(ctx context.Context, release, chart, namespace, valuesYAML string, wait bool) (execctl.Result, error) {
	return s.UpgradeHelm(ctx, HelmUpgradeInput{Namespace: namespace, Release: release, Chart: chart, ValuesYAML: valuesYAML, Wait: wait})
}

func (s *ClusterService) requireNamespace(namespace string) error {
	return validateName(namespace, "namespace")
}

func validateName(value, field string) error {
	if value == "" || len(value) > 63 || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return fault.Invalid("%s must be a DNS label", field)
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return fault.Invalid("%s must be a lowercase DNS label", field)
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
	return ValidateManifest(manifest, namespace)
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
