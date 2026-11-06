package api

import (
	"context"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	k8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

// Operations normalizes application results once, independently of transport.
// Validation and defaults remain in Service before any Kubernetes call.
type Operations struct{ Service }

func items[T any](v []T, err error) (ItemsOutput[T], error) {
	if v == nil {
		v = []T{}
	}
	return ItemsOutput[T]{Items: v}, err
}
func (o Operations) ClusterStatus(ctx context.Context, _ EmptyInput) (k8s.ClusterStatus, error) {
	return o.Status(ctx)
}
func (o Operations) Namespaces(ctx context.Context, _ EmptyInput) (ItemsOutput[k8s.NamespaceSummary], error) {
	return items(o.ListNamespaces(ctx))
}
func (o Operations) StorageClasses(ctx context.Context, _ EmptyInput) (ItemsOutput[k8s.StorageClassSummary], error) {
	return items(o.ListStorageClasses(ctx))
}
func (o Operations) Resources(ctx context.Context, in NamespaceInput) (k8s.NamespaceResources, error) {
	return o.ListResources(ctx, in.Namespace)
}
func (o Operations) GatewayResources(ctx context.Context, in NamespaceInput) (k8s.GatewaySnapshot, error) {
	return o.ListGatewayResources(ctx, in.Namespace)
}
func (o Operations) Rollout(ctx context.Context, in RolloutInput) (k8s.RolloutStatus, error) {
	return o.GetRolloutStatus(ctx, in.Namespace, in.Kind, in.Name)
}
func (o Operations) Validate(ctx context.Context, in ManifestInput) (ManifestOutput, error) {
	r, e := o.ValidateManifest(ctx, in.Manifest, in.Namespace)
	return ManifestOutput{Valid: e == nil, Resources: r}, e
}
func (o Operations) Resource(ctx context.Context, in ResourceInput) (k8s.ResourceDetail, error) {
	return o.GetResource(ctx, in.Namespace, in.Kind, in.Name)
}
func (o Operations) YAML(ctx context.Context, in ResourceYAMLInput) (k8s.ResourceYAML, error) {
	return o.GetResourceYAML(ctx, in.Namespace, in.Kind, in.Name)
}
func (o Operations) Objects(ctx context.Context, in ListInput) (k8s.ResourcePage, error) {
	return o.ListResourceObjects(ctx, in.Namespace, in.Kind, in.LabelSelector, in.Continue, in.Limit)
}
func (o Operations) Events(ctx context.Context, in EventsInput) (k8s.EventPage, error) {
	return o.ListEvents(ctx, in.Namespace, in.Kind, in.Name, in.Continue, in.Limit)
}
func (o Operations) History(ctx context.Context, in HistoryInput) (k8s.WorkloadHistory, error) {
	return o.GetWorkloadHistory(ctx, in.Namespace, in.Kind, in.Name, in.Continue, in.Limit)
}
func (o Operations) HelmList(ctx context.Context, in HelmListInput) (ItemsOutput[execctl.HelmRelease], error) {
	return items(o.ListHelmReleases(ctx, in.Namespace, in.Limit, in.Offset))
}
func (o Operations) HelmStatus(ctx context.Context, in HelmReleaseInput) (execctl.HelmRelease, error) {
	return o.GetHelmStatus(ctx, in.Namespace, in.Release)
}
func (o Operations) HelmHistory(ctx context.Context, in HelmHistoryInput) (ItemsOutput[execctl.HelmRevision], error) {
	return items(o.GetHelmHistory(ctx, in.Namespace, in.Release, in.Limit))
}
func (o Operations) Apply(ctx context.Context, in ApplyInput) (ApplyOutput, error) {
	return o.apply(ctx, in, false)
}
func (o Operations) DryRun(ctx context.Context, in ApplyInput) (ApplyOutput, error) {
	return o.apply(ctx, in, true)
}
func (o Operations) apply(ctx context.Context, in ApplyInput, dryRun bool) (ApplyOutput, error) {
	r, rs, e := o.ApplyManifest(ctx, in.Manifest, in.Namespace, in.ServerSide, dryRun)
	return ApplyOutput{Resources: rs, Result: r, DryRun: dryRun}, e
}
func (o Operations) Scale(ctx context.Context, in ScaleInput) (k8s.MutationResult, error) {
	return o.ScaleWorkload(ctx, in.Namespace, in.Kind, in.Name, in.Replicas)
}
func (o Operations) Restart(ctx context.Context, in ResourceInput) (k8s.MutationResult, error) {
	return o.RestartWorkload(ctx, in.Namespace, in.Kind, in.Name)
}
func (o Operations) Rollback(ctx context.Context, in RollbackInput) (execctl.Result, error) {
	return o.RollbackWorkload(ctx, in.Namespace, in.Kind, in.Name, in.Revision)
}
func (o Operations) Delete(ctx context.Context, in ResourceInput) (k8s.MutationResult, error) {
	return o.DeleteResource(ctx, in.Namespace, in.Kind, in.Name)
}
func (o Operations) HelmRollback(ctx context.Context, in HelmRollbackInput) (execctl.Result, error) {
	return o.RollbackHelm(ctx, in.Namespace, in.Release, in.Revision, in.Wait)
}
func (o Operations) HelmUninstall(ctx context.Context, in HelmUninstallInput) (execctl.Result, error) {
	return o.UninstallHelm(ctx, in.Namespace, in.Release, in.Wait)
}
func (o Operations) NamespaceCreate(ctx context.Context, in NamespaceInput) (k8s.MutationResult, error) {
	return o.CreateNamespace(ctx, in.Namespace)
}
func (o Operations) NamespaceDelete(ctx context.Context, in NamespaceInput) (k8s.MutationResult, error) {
	return o.DeleteNamespace(ctx, in.Namespace)
}
func (o Operations) NamespaceMetadata(ctx context.Context, in NamespaceMetadataInput) (k8s.MutationResult, error) {
	return o.UpdateNamespaceMetadata(ctx, in.Namespace, application.NamespaceMetadataChanges{Labels: in.Labels, Annotations: in.Annotations, RemoveLabels: in.RemoveLabels, RemoveAnnotations: in.RemoveAnnotations})
}
func (o Operations) ConfigMap(ctx context.Context, in ConfigMapInput) (k8s.MutationResult, error) {
	return o.UpdateConfigMap(ctx, in.Namespace, in.Name, in.Data)
}
