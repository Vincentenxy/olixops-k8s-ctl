package domain

import "context"

// PodExecutor delegates server-constructed argv to the private Kubernetes MCP Server.
type PodExecutor interface {
	Execute(context.Context, string, string, string, []string) (PodExecOutput, error)
}
type PodExecInput struct {
	Command        string `json:"command,omitempty" jsonschema:"Shell command for action=shell only, executed inside the target container using /bin/sh -c"`
	Namespace      string `json:"namespace"`
	Pod            string `json:"pod"`
	Container      string `json:"container"`
	Action         string `json:"action" jsonschema:"Use shell for a single arbitrary command, or an existing fixed action"`
	Path           string `json:"path,omitempty" jsonschema:"Exact administrator-approved file path for read_file"`
	PID            int    `json:"pid,omitempty" jsonschema:"PID for process_status; defaults to 1"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}
type PodExecOutput struct {
	Output        string `json:"output"`
	UpstreamError bool   `json:"upstreamError"`
	Truncated     bool   `json:"truncated"`
	TimedOut      bool   `json:"timedOut"`
	Outcome       string `json:"outcome"`
	PodUID        string `json:"podUID,omitempty"`
	DurationMS    int64  `json:"durationMs"`
	// Upstream v0.1.2 returns text, not distinct streams or a machine-readable exit code.
}
