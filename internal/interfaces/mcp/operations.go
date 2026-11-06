package mcp

import (
	"context"
	"olixops-k8s-ctl/internal/application/api"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

type resourceInput = api.ResourceInput
type resourceYAMLInput = api.ResourceYAMLInput
type listInput = api.ListInput
type eventsInput = api.EventsInput
type helmListInput = api.HelmListInput
type helmReleaseInput = api.HelmReleaseInput
type helmHistoryInput = api.HelmHistoryInput

type historyInput = api.HistoryInput

func registerReads(server *mcp.Server, service Service) {
	register(server, "get_workload_history", "List workload revision numbers from owned ReplicaSets or ControllerRevisions, without historical Pod templates", (api.Operations{Service: service}).History)
	register(server, "get_resource", "Get a resource with annotations, ConfigMap values and literal environment/command values omitted or redacted", (api.Operations{Service: service}).Resource)
	register(server, "get_resource_yaml", "Get the raw YAML of an allowlisted resource; policy and field redaction are reserved for a later authorization layer", (api.Operations{Service: service}).YAML)
	register(server, "list_resource_objects", "List allowlisted resource details, filtered by labels and paginated", (api.Operations{Service: service}).Objects)
	register(server, "list_events", "List namespace events, optionally filtered by resource, with pagination", (api.Operations{Service: service}).Events)
	registerPodLogs(server, service)
	register(server, "list_helm_releases", "List release summaries in one namespace, without values, manifests or NOTES", (api.Operations{Service: service}).HelmList)
	register(server, "get_helm_status", "Get Helm release status without values, manifests or NOTES", (api.Operations{Service: service}).HelmStatus)
	register(server, "get_helm_history", "Get Helm revision summaries without configuration or descriptions", (api.Operations{Service: service}).HelmHistory)
}

type applyInput = api.ApplyInput
type applyOutput = api.ApplyOutput
type scaleInput = api.ScaleInput
type rollbackInput = api.RollbackInput
type helmRollbackInput = api.HelmRollbackInput
type helmUninstallInput = api.HelmUninstallInput
type configMapInput = api.ConfigMapInput
type namespaceMetadataInput = api.NamespaceMetadataInput

func registerWrites(server *mcp.Server, service Service, timeout time.Duration) {
	for _, dryRun := range []bool{true, false} {
		name, description := "apply_manifest", "Create or update allowlisted namespaced resources from YAML; does not wait for readiness; multi-document apply is not atomic"
		if dryRun {
			name, description = "dry_run_manifest", "Run server-side validation without persisting changes; requires write capability"
		}
		registerTool(server, name, description, false, !dryRun, timeout, func(ctx context.Context, in applyInput) (applyOutput, error) {
			o := api.Operations{Service: service}
			if dryRun {
				return o.DryRun(ctx, in)
			}
			return o.Apply(ctx, in)
		})
	}
	registerTool(server, "helm_upgrade", "Install or upgrade an existing-namespace release using a downloaded, preflight-checked chart; hooks disabled", false, true, timeout, func(ctx context.Context, in application.HelmUpgradeInput) (execctl.Result, error) {
		return service.UpgradeHelm(ctx, in)
	})
	registerTool(server, "scale_workload", "Set Deployment or StatefulSet desired replicas, including scaling to zero", false, true, timeout, (api.Operations{Service: service}).Scale)
	registerTool(server, "restart_workload", "Request a Deployment, StatefulSet or DaemonSet rollout restart; repeated calls restart again", false, true, timeout, (api.Operations{Service: service}).Restart)
	registerTool(server, "rollback_workload", "Request rollback to an explicit workload revision; does not wait for readiness", false, true, timeout, (api.Operations{Service: service}).Rollback)
	registerTool(server, "delete_resource", "Delete a single named resource from the allowlist with UID precondition; background deletion, no bulk delete", false, true, timeout, (api.Operations{Service: service}).Delete)
	registerTool(server, "helm_rollback", "Roll back a release to an explicit positive revision after manifest checks; hooks disabled", false, true, timeout, (api.Operations{Service: service}).HelmRollback)
	registerTool(server, "helm_uninstall", "Uninstall a release and its resources after manifest checks; hooks disabled", false, true, timeout, (api.Operations{Service: service}).HelmUninstall)
	registerTool(server, "create_namespace", "Create one namespace within the configured scope", false, false, timeout, (api.Operations{Service: service}).NamespaceCreate)
	registerTool(server, "delete_namespace", "Delete a namespace and its contents; default and kube-* namespaces are protected", false, true, timeout, (api.Operations{Service: service}).NamespaceDelete)
	registerTool(server, "update_namespace_metadata", "Update only a Namespace's labels and annotations; other Namespace fields cannot be changed", false, true, timeout, (api.Operations{Service: service}).NamespaceMetadata)
	registerTool(server, "update_configmap", "Merge data keys into an existing ConfigMap; does not restart workloads or return data", false, true, timeout, (api.Operations{Service: service}).ConfigMap)
}

func registerPodLogs(server *mcp.Server, service Service) {
	register(server, "get_pod_logs", "Get bounded Pod container logs (no streaming). Logs may contain application-sensitive data", func(ctx context.Context, in clusterk8s.LogOptions) (clusterk8s.PodLogs, error) {
		return service.GetPodLogs(ctx, in)
	})
}
