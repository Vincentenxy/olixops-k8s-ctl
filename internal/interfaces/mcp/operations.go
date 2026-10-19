package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

type resourceInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}
type listInput struct {
	Namespace     string `json:"namespace"`
	Kind          string `json:"kind"`
	LabelSelector string `json:"labelSelector,omitempty"`
	Continue      string `json:"continue,omitempty"`
	Limit         int64  `json:"limit,omitempty" jsonschema:"Page size 1-200, default 100"`
}
type eventsInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Continue  string `json:"continue,omitempty"`
	Limit     int64  `json:"limit,omitempty"`
}
type helmListInput struct {
	Namespace string `json:"namespace"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}
type helmReleaseInput struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
}
type helmHistoryInput struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
	Limit     int    `json:"limit,omitempty"`
}

type historyInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Continue  string `json:"continue,omitempty"`
	Limit     int64  `json:"limit,omitempty"`
}

func registerReads(server *mcp.Server, service Service) {
	register(server, "get_workload_history", "List workload revision numbers from owned ReplicaSets or ControllerRevisions, without historical Pod templates", func(ctx context.Context, in historyInput) (clusterk8s.WorkloadHistory, error) {
		return service.GetWorkloadHistory(ctx, in.Namespace, in.Kind, in.Name, in.Continue, in.Limit)
	})
	register(server, "get_resource", "Get a resource with annotations, ConfigMap values and literal environment/command values omitted or redacted", func(ctx context.Context, in resourceInput) (clusterk8s.ResourceDetail, error) {
		return service.GetResource(ctx, in.Namespace, in.Kind, in.Name)
	})
	register(server, "list_resource_objects", "List allowlisted resource details, filtered by labels and paginated", func(ctx context.Context, in listInput) (clusterk8s.ResourcePage, error) {
		return service.ListResourceObjects(ctx, in.Namespace, in.Kind, in.LabelSelector, in.Continue, in.Limit)
	})
	register(server, "list_events", "List namespace events, optionally filtered by resource, with pagination", func(ctx context.Context, in eventsInput) (clusterk8s.EventPage, error) {
		return service.ListEvents(ctx, in.Namespace, in.Kind, in.Name, in.Continue, in.Limit)
	})
	register(server, "get_pod_logs", "Get bounded Pod container logs (no streaming). Logs may contain application-sensitive data", func(ctx context.Context, in clusterk8s.LogOptions) (clusterk8s.PodLogs, error) {
		return service.GetPodLogs(ctx, in)
	})
	register(server, "list_helm_releases", "List release summaries in one namespace, without values, manifests or NOTES", func(ctx context.Context, in helmListInput) (itemsOutput[execctl.HelmRelease], error) {
		items, err := service.ListHelmReleases(ctx, in.Namespace, in.Limit, in.Offset)
		return itemsOutput[execctl.HelmRelease]{Items: nonNil(items)}, err
	})
	register(server, "get_helm_status", "Get Helm release status without values, manifests or NOTES", func(ctx context.Context, in helmReleaseInput) (execctl.HelmRelease, error) {
		return service.GetHelmStatus(ctx, in.Namespace, in.Release)
	})
	register(server, "get_helm_history", "Get Helm revision summaries without configuration or descriptions", func(ctx context.Context, in helmHistoryInput) (itemsOutput[execctl.HelmRevision], error) {
		items, err := service.GetHelmHistory(ctx, in.Namespace, in.Release, in.Limit)
		return itemsOutput[execctl.HelmRevision]{Items: nonNil(items)}, err
	})
}

type applyInput struct {
	Namespace  string `json:"namespace"`
	Manifest   string `json:"manifest"`
	ServerSide bool   `json:"serverSide,omitempty"`
}
type applyOutput struct {
	Resources []application.ManifestResource `json:"resources"`
	Result    execctl.Result                 `json:"result"`
	DryRun    bool                           `json:"dryRun"`
}
type scaleInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind" jsonschema:"Deployment or StatefulSet"`
	Name      string `json:"name"`
	Replicas  int32  `json:"replicas" jsonschema:"Desired replicas from 0 to 10000"`
}
type rollbackInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Revision  int    `json:"revision" jsonschema:"Explicit positive target revision"`
}
type helmRollbackInput struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
	Revision  int    `json:"revision"`
	Wait      bool   `json:"wait,omitempty"`
}
type helmUninstallInput struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
	Wait      bool   `json:"wait,omitempty"`
}
type configMapInput struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Data      map[string]string `json:"data" jsonschema:"Merge these keys into an existing ConfigMap, without deleting other keys"`
}
type namespaceMetadataInput struct {
	Namespace         string            `json:"namespace" jsonschema:"Namespace within the configured allowlist"`
	Labels            map[string]string `json:"labels,omitempty" jsonschema:"Labels to set or replace"`
	Annotations       map[string]string `json:"annotations,omitempty" jsonschema:"Annotations to set or replace"`
	RemoveLabels      []string          `json:"removeLabels,omitempty" jsonschema:"Label keys to remove"`
	RemoveAnnotations []string          `json:"removeAnnotations,omitempty" jsonschema:"Annotation keys to remove"`
}

func registerWrites(server *mcp.Server, service Service, timeout time.Duration) {
	for _, dryRun := range []bool{true, false} {
		name, description := "apply_manifest", "Create or update allowlisted namespaced resources from YAML; does not wait for readiness; multi-document apply is not atomic"
		if dryRun {
			name, description = "dry_run_manifest", "Run server-side validation without persisting changes; requires write capability"
		}
		registerTool(server, name, description, false, !dryRun, timeout, func(ctx context.Context, in applyInput) (applyOutput, error) {
			result, resources, err := service.ApplyManifest(ctx, in.Manifest, in.Namespace, in.ServerSide, dryRun)
			return applyOutput{Resources: resources, Result: result, DryRun: dryRun}, err
		})
	}
	registerTool(server, "helm_upgrade", "Install or upgrade an existing-namespace release using a downloaded, preflight-checked chart; hooks disabled", false, true, timeout, func(ctx context.Context, in application.HelmUpgradeInput) (execctl.Result, error) {
		return service.UpgradeHelm(ctx, in)
	})
	registerTool(server, "scale_workload", "Set Deployment or StatefulSet desired replicas, including scaling to zero", false, true, timeout, func(ctx context.Context, in scaleInput) (clusterk8s.MutationResult, error) {
		return service.ScaleWorkload(ctx, in.Namespace, in.Kind, in.Name, in.Replicas)
	})
	registerTool(server, "restart_workload", "Request a Deployment, StatefulSet or DaemonSet rollout restart; repeated calls restart again", false, true, timeout, func(ctx context.Context, in resourceInput) (clusterk8s.MutationResult, error) {
		return service.RestartWorkload(ctx, in.Namespace, in.Kind, in.Name)
	})
	registerTool(server, "rollback_workload", "Request rollback to an explicit workload revision; does not wait for readiness", false, true, timeout, func(ctx context.Context, in rollbackInput) (execctl.Result, error) {
		return service.RollbackWorkload(ctx, in.Namespace, in.Kind, in.Name, in.Revision)
	})
	registerTool(server, "delete_resource", "Delete a single named resource from the allowlist with UID precondition; background deletion, no bulk delete", false, true, timeout, func(ctx context.Context, in resourceInput) (clusterk8s.MutationResult, error) {
		return service.DeleteResource(ctx, in.Namespace, in.Kind, in.Name)
	})
	registerTool(server, "helm_rollback", "Roll back a release to an explicit positive revision after manifest checks; hooks disabled", false, true, timeout, func(ctx context.Context, in helmRollbackInput) (execctl.Result, error) {
		return service.RollbackHelm(ctx, in.Namespace, in.Release, in.Revision, in.Wait)
	})
	registerTool(server, "helm_uninstall", "Uninstall a release and its resources after manifest checks; hooks disabled", false, true, timeout, func(ctx context.Context, in helmUninstallInput) (execctl.Result, error) {
		return service.UninstallHelm(ctx, in.Namespace, in.Release, in.Wait)
	})
	registerTool(server, "create_namespace", "Create one namespace within the configured scope", false, false, timeout, func(ctx context.Context, in namespaceInput) (clusterk8s.MutationResult, error) {
		return service.CreateNamespace(ctx, in.Namespace)
	})
	registerTool(server, "delete_namespace", "Delete a namespace and its contents; default and kube-* namespaces are protected", false, true, timeout, func(ctx context.Context, in namespaceInput) (clusterk8s.MutationResult, error) {
		return service.DeleteNamespace(ctx, in.Namespace)
	})
	registerTool(server, "update_namespace_metadata", "Update only a Namespace's labels and annotations; other Namespace fields cannot be changed", false, true, timeout, func(ctx context.Context, in namespaceMetadataInput) (clusterk8s.MutationResult, error) {
		return service.UpdateNamespaceMetadata(ctx, in.Namespace, application.NamespaceMetadataChanges{
			Labels:            in.Labels,
			Annotations:       in.Annotations,
			RemoveLabels:      in.RemoveLabels,
			RemoveAnnotations: in.RemoveAnnotations,
		})
	})
	registerTool(server, "update_configmap", "Merge data keys into an existing ConfigMap; does not restart workloads or return data", false, true, timeout, func(ctx context.Context, in configMapInput) (clusterk8s.MutationResult, error) {
		return service.UpdateConfigMap(ctx, in.Namespace, in.Name, in.Data)
	})
}
