package mcp

import (
	"context"
	"errors"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"olixops-k8s-ctl/internal/application/api"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"olixops-k8s-ctl/internal/domain"
	"olixops-k8s-ctl/pkg/logger"
)

type TerminalService interface {
	OpenPodTerminal(context.Context, domain.TerminalOpenInput) (domain.TerminalResult, error)
	WritePodTerminal(context.Context, domain.TerminalWriteInput) (domain.TerminalResult, error)
	ReadPodTerminal(context.Context, domain.TerminalReadInput) (domain.TerminalResult, error)
	ClosePodTerminal(context.Context, domain.TerminalSessionInput) (domain.TerminalResult, error)
	DownloadPodFile(context.Context, domain.DownloadInput) (domain.DownloadOutput, error)
	UploadPodFile(context.Context, domain.UploadInput) (domain.UploadOutput, error)
}

func registerTerminals(server *sdk.Server, s TerminalService) {
	terminalTool(server, "upload_pod_file", "Prepare a five-minute, single-use uploadId/uploadUrl bound to your user and Pod UID. HTTP POST raw file bytes with the same Bearer credential and X-Upload-Id header; curl --data-binary @local-file with Content-Length (chunked uploads unsupported). Creates one new 0600 file in an existing writable directory; never overwrites. No fixed size limit, resume, directory upload or automatic reload. Container needs /bin/sh, head (-c), wc, sha256sum, mkdir, rmdir, rm, ln. Commit only after complete transfer and remote size/SHA-256 verification. No file bytes in MCP.", s.UploadPodFile)
	terminalTool(server, "open_pod_terminal", "Start a user-owned /bin/sh session. Optional tty=true merges streams; open means stream starting, read state to detect failure. Not SSH. Sessions expire and do not survive server restart.", s.OpenPodTerminal)
	terminalTool(server, "write_pod_terminal", "Send input to your terminal; include newline to execute. Input may be control characters such as U+0003 for Ctrl+C (TTY required). Sent is not confirmation of remote process termination.", s.WritePodTerminal)
	terminalTool(server, "read_pod_terminal", "Read up to 64 KiB of binary-safe outputBase64 with a byte cursor; waitSeconds 0-5. lostBytes reports overwritten ring-buffer data. Streams merged; ANSI terminal sequences retained.", s.ReadPodTerminal)
	terminalTool(server, "close_pod_terminal", "Close your session and release its connection. Can close after Pod deletion; remote process termination is not guaranteed.", s.ClosePodTerminal)
	terminalTool(server, "download_pod_file", "Prepare a single-use downloadId and HTTP POST downloadUrl, valid for five minutes and bound to the authenticated user and Pod UID. Use curl with the same Bearer credential and JSON {downloadId:...}, saving the binary response locally. No file bytes in MCP. No fixed size limit or resume. Container requires tar. ConfigMap symlinks are followed. Relative URLs use the same service and gateway prefix as this MCP.", s.DownloadPodFile)
}
func terminalTool[In, Out any](server *sdk.Server, name, description string, call func(context.Context, In) (Out, error)) {
	destructive := true
	sdk.AddTool(server, &sdk.Tool{Name: name, Description: description, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive}}, func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, Out, error) {
		ctx, cancel := context.WithTimeout(ctx, api.ShellTimeout)
		defer cancel()
		out, err := call(ctx, in)
		logger.Info(ctx, "mcp shell operation", zap.String("tool", name), zap.Bool("success", err == nil))
		if err != nil {
			var rejection *interceptor.Rejection
			var zero Out
			if errors.As(err, &rejection) {
				return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: rejection.Error()}}}, zero, nil
			}
			return nil, zero, api.Classify(err)
		}
		return nil, out, nil
	})
}
