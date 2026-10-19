// Package mcp exposes a fixed set of application operations over MCP.
package mcp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/pkg/logger"
)

// ReadService deliberately excludes command runners and every mutating operation.
type ReadService interface {
	Status(context.Context) (clusterk8s.ClusterStatus, error)
	ListNamespaces(context.Context) ([]clusterk8s.NamespaceSummary, error)
	ListStorageClasses(context.Context) ([]clusterk8s.StorageClassSummary, error)
	ListResources(context.Context, string) (clusterk8s.NamespaceResources, error)
	ListGatewayResources(context.Context, string) (clusterk8s.GatewaySnapshot, error)
	GetRolloutStatus(context.Context, string, string, string) (clusterk8s.RolloutStatus, error)
	ValidateManifest(context.Context, string, string) ([]application.ManifestResource, error)
}

type Service interface {
	ReadService
	GetWorkloadHistory(context.Context, string, string, string, string, int64) (clusterk8s.WorkloadHistory, error)
	GetResource(context.Context, string, string, string) (clusterk8s.ResourceDetail, error)
	ListResourceObjects(context.Context, string, string, string, string, int64) (clusterk8s.ResourcePage, error)
	ListEvents(context.Context, string, string, string, string, int64) (clusterk8s.EventPage, error)
	GetPodLogs(context.Context, clusterk8s.LogOptions) (clusterk8s.PodLogs, error)
	ListHelmReleases(context.Context, string, int, int) ([]execctl.HelmRelease, error)
	GetHelmStatus(context.Context, string, string) (execctl.HelmRelease, error)
	GetHelmHistory(context.Context, string, string, int) ([]execctl.HelmRevision, error)
	ApplyManifest(context.Context, string, string, bool, bool) (execctl.Result, []application.ManifestResource, error)
	UpgradeHelm(context.Context, application.HelmUpgradeInput) (execctl.Result, error)
	ScaleWorkload(context.Context, string, string, string, int32) (clusterk8s.MutationResult, error)
	RestartWorkload(context.Context, string, string, string) (clusterk8s.MutationResult, error)
	RollbackWorkload(context.Context, string, string, string, int) (execctl.Result, error)
	DeleteResource(context.Context, string, string, string) (clusterk8s.MutationResult, error)
	RollbackHelm(context.Context, string, string, int, bool) (execctl.Result, error)
	UninstallHelm(context.Context, string, string, bool) (execctl.Result, error)
	CreateNamespace(context.Context, string) (clusterk8s.MutationResult, error)
	DeleteNamespace(context.Context, string) (clusterk8s.MutationResult, error)
	UpdateNamespaceMetadata(context.Context, string, application.NamespaceMetadataChanges) (clusterk8s.MutationResult, error)
	UpdateConfigMap(context.Context, string, string, map[string]string) (clusterk8s.MutationResult, error)
}

type Options struct {
	WriteEnabled bool
	WriteTimeout time.Duration
}

type emptyInput struct{}
type namespaceInput struct {
	Namespace string `json:"namespace" jsonschema:"Namespace within the configured allowlist"`
}
type rolloutInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind" jsonschema:"Deployment, StatefulSet or DaemonSet"`
	Name      string `json:"name"`
}
type manifestInput struct {
	Namespace string `json:"namespace"`
	Manifest  string `json:"manifest" jsonschema:"YAML to validate locally; never applied to the cluster"`
}
type itemsOutput[T any] struct {
	Items []T `json:"items"`
}
type manifestOutput struct {
	Valid     bool                           `json:"valid"`
	Resources []application.ManifestResource `json:"resources"`
}

// NewHandler serves configured tools over stateless Streamable HTTP.
// Options are feature switches, NOT authorization. Policy is reserved in application.
// REST envelopes are not used here: MCP requires JSON-RPC responses.
func NewHandler(service Service, version string, options ...Options) http.Handler {
	opts := Options{WriteTimeout: 120 * time.Second}
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 120 * time.Second
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "olixops-k8s-ctl", Version: version}, nil)
	register(server, "cluster_status", "Get cluster version and count of visible namespaces", func(ctx context.Context, _ emptyInput) (clusterk8s.ClusterStatus, error) {
		return service.Status(ctx)
	})
	register(server, "list_namespaces", "List namespaces within the configured scope", func(ctx context.Context, _ emptyInput) (itemsOutput[clusterk8s.NamespaceSummary], error) {
		items, err := service.ListNamespaces(ctx)
		return itemsOutput[clusterk8s.NamespaceSummary]{Items: nonNil(items)}, err
	})
	register(server, "list_storage_classes", "List storage class summaries", func(ctx context.Context, _ emptyInput) (itemsOutput[clusterk8s.StorageClassSummary], error) {
		items, err := service.ListStorageClasses(ctx)
		return itemsOutput[clusterk8s.StorageClassSummary]{Items: nonNil(items)}, err
	})
	register(server, "list_resources", "List workload, Pod, Service and PVC summaries in a namespace", func(ctx context.Context, in namespaceInput) (clusterk8s.NamespaceResources, error) {
		return service.ListResources(ctx, in.Namespace)
	})
	register(server, "list_gateway_resources", "List GatewayClass, Gateway, HTTPRoute and TCPRoute summaries", func(ctx context.Context, in namespaceInput) (clusterk8s.GatewaySnapshot, error) {
		return service.ListGatewayResources(ctx, in.Namespace)
	})
	register(server, "get_rollout_status", "Get a Deployment, StatefulSet or DaemonSet rollout snapshot without waiting", func(ctx context.Context, in rolloutInput) (clusterk8s.RolloutStatus, error) {
		return service.GetRolloutStatus(ctx, in.Namespace, in.Kind, in.Name)
	})
	register(server, "validate_manifest", "Validate YAML structure and local resource/namespace policy only; not Kubernetes schema validation or server dry-run", func(ctx context.Context, in manifestInput) (manifestOutput, error) {
		resources, err := service.ValidateManifest(ctx, in.Manifest, in.Namespace)
		return manifestOutput{Valid: err == nil, Resources: resources}, err
	})
	registerReads(server, service)
	if opts.WriteEnabled {
		registerWrites(server, service, opts.WriteTimeout)
	}
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:           true,
		JSONResponse:        true,
		MaxRequestBodyBytes: 4 << 20,
	})
	return http.NewCrossOriginProtection().Handler(transport)
}

func register[In, Out any](server *mcp.Server, name, description string, call func(context.Context, In) (Out, error)) {
	registerTool(server, name, description, true, false, 30*time.Second, call)
}
func registerTool[In, Out any](server *mcp.Server, name, description string, readOnly, destructive bool, timeout time.Duration, call func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
			start := time.Now()
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			out, err := call(ctx, in)
			logger.Info(ctx, "mcp tool call", zap.String("tool", name), zap.Bool("write", !readOnly), zap.Bool("success", err == nil), zap.Duration("cost", time.Since(start)))
			if err != nil {
				// Kubernetes/parse errors can include credentials, addresses or submitted YAML.
				// Never forward or log their raw contents on this anonymous endpoint.
				var zero Out
				return nil, zero, fmt.Errorf("%s failed: check input, feature switches, configured scope, policy hook and cluster availability", name)
			}
			return nil, out, nil
		})
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
