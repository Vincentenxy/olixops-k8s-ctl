package mcp

import (
	"context"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	app "olixops-k8s-ctl/internal/application/catalog"
)

func registerCatalog(server *sdk.Server, s *app.Service, write bool, timeout time.Duration) {
	register(server, "list_service_templates", "List immutable standard Redis/PostgreSQL template versions, defaults and required parameter schema", func(ctx context.Context, _ emptyInput) (any, error) { return s.Templates(ctx) })
	register(server, "get_service_template", "Read one exact template version from SQLite", s.Template)
	register(server, "preview_service_config", "Render standard configuration without saving, reserving a port or changing Kubernetes", s.PreviewConfig)
	register(server, "list_service_instances", "List catalog instances; ownerId is unverified ownership metadata", s.Instances)
	register(server, "get_service_instance", "Read the stable catalog instance identity and revision/deployment pointers", s.Instance)
	register(server, "list_service_revisions", "Read immutable config revisions and original rendered YAML", s.Revisions)
	register(server, "get_service_deployment", "Read saved deployment and current Kubernetes readiness; no TCP or database protocol verification is implied", s.Deployment)
	register(server, "verify_service_access", "Read-only server-side Redis AUTH/PING or PostgreSQL SELECT 1 through this deployment external LB port; user-network reachability remains unverified", s.VerifyAccess)
	register(server, "list_service_deployments", "Read saved deployment history and frozen YAML artifacts", s.Deployments)
	register(server, "list_gateway_ports", "List shared Higress TCP pool usage; external port is distinct from backend port and NodePort", s.Ports)
	register(server, "list_gateway_port_events", "Read historical reservation, import, binding and release events", s.PortEvents)
	if !write {
		return
	}
	registerTool(server, "save_service_template", "Save a NEW immutable template version; does not deploy it", false, false, timeout, s.SaveTemplate)
	registerTool(server, "save_service_config", "Create independent instance or append immutable config revision; expectedRevision and requestId required; no port or cluster changes", false, false, timeout, s.SaveConfig)
	registerTool(server, "sync_gateway_ports", "WRITE SQLite ledger after fully reading Higress Service, Gateway and all related TCPRoutes; existing routes stay externally owned", false, false, timeout, func(ctx context.Context, _ emptyInput) (any, error) { return s.SyncPorts(ctx) })
	registerTool(server, "reserve_gateway_port", "WRITE ledger: atomically reserve one available external TCP port for an existing instance", false, false, timeout, s.ReservePort)
	registerTool(server, "release_gateway_port", "WRITE ledger: release only after actual route and deployment checks; active or unknown deployments retain their ports", false, true, timeout, s.ReleasePort)
	registerTool(server, "prepare_service_deployment", "WRITE ledger: freeze revision/YAML/hash, reserve external port and return deploymentId; no Kubernetes write", false, false, timeout, s.Prepare)
	registerTool(server, "deploy_service", "Deploy frozen standard artifact by deploymentId: server dry-run, incremental shared Higress exposure, apply owned instance resources. Retry SAME id after interruption; port retained on failure", false, true, timeout, s.Deploy)
	registerTool(server, "delete_service_instance", "Delete owned instance resources using recorded UIDs; retain PVC and shared ingress; reclaim port only after route removal is confirmed", false, true, timeout, s.DeleteInstance)
}
