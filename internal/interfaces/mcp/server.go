// Package mcp exposes a fixed set of application operations over MCP.
package mcp

import (
	"context"
	"net/http"
	"olixops-k8s-ctl/internal/application/api"
	catalogapp "olixops-k8s-ctl/internal/application/catalog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"olixops-k8s-ctl/pkg/logger"
)

// ReadService deliberately excludes command runners and every mutating operation.
type ReadService = api.ReadService

type Service = api.Service

type Options struct {
	ShellEnabled    bool
	DiagnosticsOnly bool
	PodExecEnabled  bool
	Catalog         *catalogapp.Service
	WriteEnabled    bool
	WriteTimeout    time.Duration
}

type emptyInput = api.EmptyInput
type namespaceInput = api.NamespaceInput
type rolloutInput = api.RolloutInput
type manifestInput = api.ManifestInput
type itemsOutput[T any] = api.ItemsOutput[T]
type manifestOutput = api.ManifestOutput

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
	if opts.DiagnosticsOnly {
		registerPodLogs(server, service)
		if opts.WriteEnabled && opts.PodExecEnabled {
			if exec, ok := service.(PodExecService); ok {
				registerPodExec(server, exec)
			}
		}
	} else {
		register(server, "cluster_status", "Get cluster version and count of visible namespaces", (api.Operations{Service: service}).ClusterStatus)
		register(server, "list_namespaces", "List namespaces within the configured scope", (api.Operations{Service: service}).Namespaces)
		register(server, "list_storage_classes", "List storage class summaries", (api.Operations{Service: service}).StorageClasses)
		register(server, "list_resources", "List workload, Pod, Service and PVC summaries in a namespace", (api.Operations{Service: service}).Resources)
		register(server, "list_gateway_resources", "List GatewayClass, Gateway, HTTPRoute and TCPRoute summaries", (api.Operations{Service: service}).GatewayResources)
		register(server, "get_rollout_status", "Get a Deployment, StatefulSet or DaemonSet rollout snapshot without waiting", (api.Operations{Service: service}).Rollout)
		register(server, "validate_manifest", "Validate YAML structure and local resource/namespace policy only; not Kubernetes schema validation or server dry-run", (api.Operations{Service: service}).Validate)
		registerReads(server, service)
		if opts.WriteEnabled {
			registerWrites(server, service, opts.WriteTimeout)
			if opts.PodExecEnabled {
				if exec, ok := service.(PodExecService); ok {
					registerPodExec(server, exec)
					if terminal, ok := service.(TerminalService); ok && opts.ShellEnabled {
						registerTerminals(server, terminal)
					}
				}
			}
		}
		if opts.Catalog != nil {
			registerCatalog(server, opts.Catalog, opts.WriteEnabled, opts.WriteTimeout)
		}
	}
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:           true,
		JSONResponse:        true,
		MaxRequestBodyBytes: 4 << 20,
	})
	return http.NewCrossOriginProtection().Handler(boundJSONBody(transport))
}

func register[In, Out any](server *mcp.Server, name, description string, call func(context.Context, In) (Out, error)) {
	registerTool(server, name, description, true, false, api.ReadTimeout, call)
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
				var zero Out
				failure := api.Classify(err)
				return nil, zero, failure
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
