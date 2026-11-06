package mcp

import (
	"context"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"olixops-k8s-ctl/internal/application/api"
	"olixops-k8s-ctl/internal/domain"
)

type PodExecService interface {
	ExecPodCommand(context.Context, domain.PodExecInput) (domain.PodExecOutput, error)
}

func registerPodExec(server *sdk.Server, service PodExecService) {
	destructive := true
	sdk.AddTool(server, &sdk.Tool{Name: "exec_pod_command", Description: "Run a single command with action=shell and command=<shell text> when enabled, or a fixed action, in an explicitly opted-in development Pod through the private Kubernetes MCP Server. Actions: redis_ping, nginx_check, disk_usage, process_list, environment, read_file, process_status, memory_usage. read_file requires an exact administrator-approved path; process_status accepts a numeric PID. Shell uses /bin/sh -c inside the target container and the configured interceptor; no interactive stdin or TTY. Returns upstream text, not separate streams or an exit code. Timeout may leave the remote process running; do not blindly retry.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive}}, func(ctx context.Context, _ *sdk.CallToolRequest, in domain.PodExecInput) (*sdk.CallToolResult, domain.PodExecOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, api.ShellTimeout)
		defer cancel()
		out, err := service.ExecPodCommand(ctx, in)
		if failure := api.ExecFailure(out, err); failure != nil {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: api.Classify(failure).Error()}}}, out, nil
		}
		return &sdk.CallToolResult{IsError: out.UpstreamError || out.TimedOut}, out, nil
	})
}
