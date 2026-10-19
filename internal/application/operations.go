package application

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"olixops-k8s-ctl/internal/domain"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

type NamespaceMetadataChanges struct {
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	RemoveLabels      []string          `json:"removeLabels,omitempty"`
	RemoveAnnotations []string          `json:"removeAnnotations,omitempty"`
}

func (s *ClusterService) resourceTarget(namespace, kind, name string) (domain.Target, error) {
	target := domain.Target{Namespace: namespace, Kind: kind, Name: name}
	if err := s.requireNamespace(namespace); err != nil {
		return target, err
	}
	resource, ok := domain.LookupResource(kind)
	if !ok {
		return target, fmt.Errorf("unsupported resource kind")
	}
	target.APIVersion = resource.Versions[0]
	if resource.Group != "" {
		target.APIVersion = resource.Group + "/" + target.APIVersion
	}
	if name != "" && len(validation.IsDNS1123Subdomain(name)) > 0 {
		return target, fmt.Errorf("invalid resource name")
	}
	return target, nil
}
func (s *ClusterService) namedTarget(namespace, kind, name string) (domain.Target, error) {
	if name == "" {
		return domain.Target{}, fmt.Errorf("name is required")
	}
	return s.resourceTarget(namespace, kind, name)
}
func (s *ClusterService) GetResource(ctx context.Context, namespace, kind, name string) (clusterk8s.ResourceDetail, error) {
	target, err := s.namedTarget(namespace, kind, name)
	if err != nil {
		return clusterk8s.ResourceDetail{}, err
	}
	if err := s.authorize(ctx, "resource.get", false, []domain.Target{target}, nil); err != nil {
		return clusterk8s.ResourceDetail{}, err
	}
	return s.client.GetResource(ctx, namespace, kind, name)
}
func pageLimit(limit int64) (int64, error) {
	if limit == 0 {
		return 100, nil
	}
	if limit < 1 || limit > 200 {
		return 0, fmt.Errorf("limit must be between 1 and 200")
	}
	return limit, nil
}
func (s *ClusterService) ListResourceObjects(ctx context.Context, namespace, kind, selector, token string, limit int64) (clusterk8s.ResourcePage, error) {
	target, err := s.resourceTarget(namespace, kind, "")
	if err != nil {
		return clusterk8s.ResourcePage{}, err
	}
	limit, err = pageLimit(limit)
	if err != nil {
		return clusterk8s.ResourcePage{}, err
	}
	if len(selector) > 1024 || len(token) > 8192 {
		return clusterk8s.ResourcePage{}, fmt.Errorf("query parameter too long")
	}
	if err := s.authorize(ctx, "resource.list", false, []domain.Target{target}, nil); err != nil {
		return clusterk8s.ResourcePage{}, err
	}
	return s.client.ListResourceObjects(ctx, namespace, kind, selector, token, limit)
}
func (s *ClusterService) ListEvents(ctx context.Context, namespace, kind, name, token string, limit int64) (clusterk8s.EventPage, error) {
	if err := s.requireNamespace(namespace); err != nil {
		return clusterk8s.EventPage{}, err
	}
	if kind != "" {
		if _, ok := domain.LookupResource(kind); !ok {
			return clusterk8s.EventPage{}, fmt.Errorf("unsupported event resource kind")
		}
	}
	if name != "" && len(validation.IsDNS1123Subdomain(name)) > 0 {
		return clusterk8s.EventPage{}, fmt.Errorf("invalid resource name")
	}
	limit, err := pageLimit(limit)
	if err != nil {
		return clusterk8s.EventPage{}, err
	}
	if len(token) > 8192 {
		return clusterk8s.EventPage{}, fmt.Errorf("continue token too long")
	}
	if err := s.authorize(ctx, "event.list", false, []domain.Target{{Kind: kind, Namespace: namespace, Name: name}}, nil); err != nil {
		return clusterk8s.EventPage{}, err
	}
	return s.client.ListEvents(ctx, namespace, kind, name, token, limit)
}
func (s *ClusterService) GetPodLogs(ctx context.Context, options clusterk8s.LogOptions) (clusterk8s.PodLogs, error) {
	target, err := s.namedTarget(options.Namespace, "Pod", options.Pod)
	if err != nil {
		return clusterk8s.PodLogs{}, err
	}
	if options.Container != "" && len(validation.IsDNS1123Label(options.Container)) > 0 {
		return clusterk8s.PodLogs{}, fmt.Errorf("invalid container name")
	}
	if options.TailLines == 0 {
		options.TailLines = 200
	}
	if options.TailLines < 1 || options.TailLines > 5000 || options.SinceSeconds < 0 || options.SinceSeconds > 604800 {
		return clusterk8s.PodLogs{}, fmt.Errorf("invalid log bounds")
	}
	if err := s.authorize(ctx, "pod.logs", false, []domain.Target{target}, nil); err != nil {
		return clusterk8s.PodLogs{}, err
	}
	return s.client.GetPodLogs(ctx, options)
}
func (s *ClusterService) DeleteResource(ctx context.Context, namespace, kind, name string) (clusterk8s.MutationResult, error) {
	target, err := s.namedTarget(namespace, kind, name)
	if err != nil {
		return clusterk8s.MutationResult{}, err
	}
	if err := s.authorize(ctx, "resource.delete", true, []domain.Target{target}, nil); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	return s.client.DeleteResource(ctx, namespace, kind, name)
}
func workloadKind(kind string, scale bool) error {
	if kind == "Deployment" || kind == "StatefulSet" || (!scale && kind == "DaemonSet") {
		return nil
	}
	return fmt.Errorf("unsupported workload kind")
}
func (s *ClusterService) ScaleWorkload(ctx context.Context, namespace, kind, name string, replicas int32) (clusterk8s.MutationResult, error) {
	target, err := s.namedTarget(namespace, kind, name)
	if err != nil {
		return clusterk8s.MutationResult{}, err
	}
	if err := workloadKind(kind, true); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	if replicas < 0 || replicas > 10000 {
		return clusterk8s.MutationResult{}, fmt.Errorf("replicas must be between 0 and 10000")
	}
	if err := s.authorize(ctx, "workload.scale", true, []domain.Target{target}, replicas); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	return s.client.ScaleWorkload(ctx, namespace, kind, name, replicas)
}
func (s *ClusterService) RestartWorkload(ctx context.Context, namespace, kind, name string) (clusterk8s.MutationResult, error) {
	target, err := s.namedTarget(namespace, kind, name)
	if err != nil {
		return clusterk8s.MutationResult{}, err
	}
	if err := workloadKind(kind, false); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	if err := s.authorize(ctx, "workload.restart", true, []domain.Target{target}, nil); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	return s.client.RestartWorkload(ctx, namespace, kind, name)
}
func (s *ClusterService) RollbackWorkload(ctx context.Context, namespace, kind, name string, revision int) (execctl.Result, error) {
	target, err := s.namedTarget(namespace, kind, name)
	if err != nil {
		return execctl.Result{}, err
	}
	if err := workloadKind(kind, false); err != nil {
		return execctl.Result{}, err
	}
	if revision < 1 {
		return execctl.Result{}, fmt.Errorf("an explicit positive revision is required")
	}
	if err := s.authorize(ctx, "workload.rollback", true, []domain.Target{target}, revision); err != nil {
		return execctl.Result{}, err
	}
	if s.runner == nil {
		return execctl.Result{}, fmt.Errorf("command runner is not configured")
	}
	return s.runner.RollbackWorkload(ctx, namespace, kind, name, revision)
}
func (s *ClusterService) UpdateConfigMap(ctx context.Context, namespace, name string, data map[string]string) (clusterk8s.MutationResult, error) {
	target, err := s.namedTarget(namespace, "ConfigMap", name)
	if err != nil {
		return clusterk8s.MutationResult{}, err
	}
	if len(data) == 0 {
		return clusterk8s.MutationResult{}, fmt.Errorf("data must contain at least one key")
	}
	for key := range data {
		if len(validation.IsConfigMapKey(key)) > 0 {
			return clusterk8s.MutationResult{}, fmt.Errorf("invalid ConfigMap key")
		}
	}
	encoded, _ := json.Marshal(data)
	if len(encoded) > 1<<20 {
		return clusterk8s.MutationResult{}, fmt.Errorf("ConfigMap data exceeds 1 MiB")
	}
	if err := s.authorize(ctx, "configmap.update", true, []domain.Target{target}, data); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	return s.client.UpdateConfigMap(ctx, namespace, name, data)
}
func (s *ClusterService) CreateNamespace(ctx context.Context, name string) (clusterk8s.MutationResult, error) {
	if err := s.requireNamespace(name); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	if err := s.authorize(ctx, "namespace.create", true, []domain.Target{{Kind: "Namespace", Name: name, Namespace: name}}, nil); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	return s.client.CreateNamespace(ctx, name)
}
func (s *ClusterService) DeleteNamespace(ctx context.Context, name string) (clusterk8s.MutationResult, error) {
	if err := s.requireNamespace(name); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	if name == "default" || strings.HasPrefix(name, "kube-") {
		return clusterk8s.MutationResult{}, fmt.Errorf("protected namespace cannot be deleted")
	}
	if err := s.authorize(ctx, "namespace.delete", true, []domain.Target{{Kind: "Namespace", Name: name, Namespace: name}}, nil); err != nil {
		return clusterk8s.MutationResult{}, err
	}
	return s.client.DeleteNamespace(ctx, name)
}

func (s *ClusterService) UpdateNamespaceMetadata(ctx context.Context, name string, changes NamespaceMetadataChanges) (clusterk8s.MutationResult, error) {
	result := clusterk8s.MutationResult{Kind: "Namespace", Name: name}
	if err := s.requireNamespace(name); err != nil {
		return result, err
	}
	if len(changes.Labels) == 0 && len(changes.Annotations) == 0 && len(changes.RemoveLabels) == 0 && len(changes.RemoveAnnotations) == 0 {
		return result, fmt.Errorf("at least one label or annotation change is required")
	}
	if err := validateNamespaceMetadata(changes); err != nil {
		return result, err
	}
	target := domain.Target{Kind: "Namespace", Namespace: name, Name: name}
	if err := s.authorize(ctx, "namespace.update", true, []domain.Target{target}, changes); err != nil {
		return result, err
	}
	return s.client.UpdateNamespaceMetadata(ctx, name, changes.Labels, changes.Annotations, changes.RemoveLabels, changes.RemoveAnnotations)
}

func validateNamespaceMetadata(changes NamespaceMetadataChanges) error {
	for key, value := range changes.Labels {
		if len(validation.IsQualifiedName(key)) > 0 {
			return fmt.Errorf("invalid Namespace label key")
		}
		if len(validation.IsValidLabelValue(value)) > 0 {
			return fmt.Errorf("invalid Namespace label value")
		}
	}
	for key, value := range changes.Annotations {
		if len(validation.IsQualifiedName(key)) > 0 {
			return fmt.Errorf("invalid Namespace annotation key")
		}
		if len(value) > 256*1024 {
			return fmt.Errorf("Namespace annotation value is too large")
		}
	}
	for _, key := range changes.RemoveLabels {
		if len(validation.IsQualifiedName(key)) > 0 {
			return fmt.Errorf("invalid Namespace label key")
		}
		if _, ok := changes.Labels[key]; ok {
			return fmt.Errorf("label cannot be set and removed in the same request")
		}
	}
	for _, key := range changes.RemoveAnnotations {
		if len(validation.IsQualifiedName(key)) > 0 {
			return fmt.Errorf("invalid Namespace annotation key")
		}
		if _, ok := changes.Annotations[key]; ok {
			return fmt.Errorf("annotation cannot be set and removed in the same request")
		}
	}
	return nil
}

type HelmUpgradeInput = execctl.HelmUpgradeOptions

var chartPattern = regexp.MustCompile(`^(oci://)?[A-Za-z0-9][A-Za-z0-9.:-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)+$`)
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$`)

func (s *ClusterService) helmTarget(namespace, release string) (domain.Target, error) {
	target := domain.Target{Kind: "HelmRelease", Namespace: namespace, Name: release}
	if err := s.requireNamespace(namespace); err != nil {
		return target, err
	}
	if release != "" {
		if err := validateName(release, "release"); err != nil {
			return target, err
		}
		if len(release) > 53 {
			return target, fmt.Errorf("Helm release name exceeds 53 characters")
		}
	}
	if s.runner == nil {
		return target, fmt.Errorf("command runner is not configured")
	}
	return target, nil
}
func (s *ClusterService) ListHelmReleases(ctx context.Context, namespace string, limit, offset int) ([]execctl.HelmRelease, error) {
	target, err := s.helmTarget(namespace, "")
	if err != nil {
		return nil, err
	}
	n, err := pageLimit(int64(limit))
	if err != nil || offset < 0 {
		return nil, fmt.Errorf("invalid Helm pagination")
	}
	if err := s.authorize(ctx, "helm.list", false, []domain.Target{target}, nil); err != nil {
		return nil, err
	}
	return s.runner.ListHelmReleases(ctx, namespace, int(n), offset)
}
func (s *ClusterService) GetHelmStatus(ctx context.Context, namespace, release string) (execctl.HelmRelease, error) {
	if release == "" {
		return execctl.HelmRelease{}, fmt.Errorf("release is required")
	}
	target, err := s.helmTarget(namespace, release)
	if err != nil {
		return execctl.HelmRelease{}, err
	}
	if err := s.authorize(ctx, "helm.status", false, []domain.Target{target}, nil); err != nil {
		return execctl.HelmRelease{}, err
	}
	return s.runner.GetHelmStatus(ctx, namespace, release)
}
func (s *ClusterService) GetHelmHistory(ctx context.Context, namespace, release string, limit int) ([]execctl.HelmRevision, error) {
	if release == "" {
		return nil, fmt.Errorf("release is required")
	}
	target, err := s.helmTarget(namespace, release)
	if err != nil {
		return nil, err
	}
	n, err := pageLimit(int64(limit))
	if err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, "helm.history", false, []domain.Target{target}, nil); err != nil {
		return nil, err
	}
	return s.runner.GetHelmHistory(ctx, namespace, release, int(n))
}
func (s *ClusterService) helmValidator(ctx context.Context, action string, releaseTarget domain.Target) func(string) error {
	return func(manifest string) error {
		resources, err := ValidateManifest(manifest, releaseTarget.Namespace, s.allowedNS)
		if err != nil {
			return err
		}
		targets := []domain.Target{releaseTarget}
		for _, resource := range resources {
			targets = append(targets, domain.Target{APIVersion: resource.APIVersion, Kind: resource.Kind, Namespace: resource.Namespace, Name: resource.Name})
		}
		return s.authorize(ctx, action, true, targets, manifest)
	}
}
func (s *ClusterService) UpgradeHelm(ctx context.Context, input HelmUpgradeInput) (execctl.Result, error) {
	if input.Release == "" {
		return execctl.Result{}, fmt.Errorf("release is required")
	}
	target, err := s.helmTarget(input.Namespace, input.Release)
	if err != nil {
		return execctl.Result{}, err
	}
	if len(input.Chart) > 512 || !chartPattern.MatchString(input.Chart) || strings.Contains(input.Chart, "..") {
		return execctl.Result{}, fmt.Errorf("chart must be repo/name or oci://registry/path; local paths and arbitrary URLs are not accepted")
	}
	if input.Version != "" && !versionPattern.MatchString(input.Version) {
		return execctl.Result{}, fmt.Errorf("invalid chart version")
	}
	if len(input.ValuesYAML) > 2<<20 {
		return execctl.Result{}, fmt.Errorf("values exceeds 2 MiB")
	}
	if err := s.authorize(ctx, "helm.upgrade", true, []domain.Target{target}, input); err != nil {
		return execctl.Result{}, err
	}
	return s.runner.HelmUpgradeChecked(ctx, input, s.helmValidator(ctx, "helm.upgrade", target))
}
func (s *ClusterService) RollbackHelm(ctx context.Context, namespace, release string, revision int, wait bool) (execctl.Result, error) {
	if release == "" || revision < 1 {
		return execctl.Result{}, fmt.Errorf("release and a positive revision are required")
	}
	target, err := s.helmTarget(namespace, release)
	if err != nil {
		return execctl.Result{}, err
	}
	if err := s.authorize(ctx, "helm.rollback", true, []domain.Target{target}, []any{revision, wait}); err != nil {
		return execctl.Result{}, err
	}
	return s.runner.HelmRollbackChecked(ctx, namespace, release, revision, wait, s.helmValidator(ctx, "helm.rollback", target))
}
func (s *ClusterService) UninstallHelm(ctx context.Context, namespace, release string, wait bool) (execctl.Result, error) {
	if release == "" {
		return execctl.Result{}, fmt.Errorf("release is required")
	}
	target, err := s.helmTarget(namespace, release)
	if err != nil {
		return execctl.Result{}, err
	}
	if err := s.authorize(ctx, "helm.uninstall", true, []domain.Target{target}, wait); err != nil {
		return execctl.Result{}, err
	}
	return s.runner.HelmUninstallChecked(ctx, namespace, release, wait, s.helmValidator(ctx, "helm.uninstall", target))
}

func (s *ClusterService) GetWorkloadHistory(ctx context.Context, namespace, kind, name, token string, limit int64) (clusterk8s.WorkloadHistory, error) {
	target, err := s.namedTarget(namespace, kind, name)
	if err != nil {
		return clusterk8s.WorkloadHistory{}, err
	}
	if err := workloadKind(kind, false); err != nil {
		return clusterk8s.WorkloadHistory{}, err
	}
	limit, err = pageLimit(limit)
	if err != nil {
		return clusterk8s.WorkloadHistory{}, err
	}
	if len(token) > 8192 {
		return clusterk8s.WorkloadHistory{}, fmt.Errorf("continue token too long")
	}
	if err := s.authorize(ctx, "workload.history", false, []domain.Target{target}, nil); err != nil {
		return clusterk8s.WorkloadHistory{}, err
	}
	return s.client.GetWorkloadHistory(ctx, namespace, kind, name, token, limit)
}
