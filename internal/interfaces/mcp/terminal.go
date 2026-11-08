package mcp

import (
	"context"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"olixops-k8s-ctl/internal/application/api"
)

type TerminalService = api.TerminalService

func registerTerminals(server *sdk.Server, s TerminalService) {
	terminalTool(server, "upload_pod_file", "Prepare a five-minute, single-use uploadId/uploadUrl bound to your user and Pod UID. HTTP POST raw file bytes with the same Bearer credential and X-Upload-Id header; curl --data-binary @local-file with Content-Length (chunked uploads unsupported). Creates one new 0600 file in an existing writable directory; never overwrites. No fixed size limit, resume, directory upload or automatic reload. Container needs /bin/sh, head (-c), wc, sha256sum, mkdir, rmdir, rm, ln. Commit only after complete transfer and remote size/SHA-256 verification. No file bytes in MCP.", s.UploadPodFile)
	terminalTool(server, "open_pod_terminal", "Start a user-owned /bin/sh session. Optional tty=true merges streams; open means stream starting, read state to detect failure. Not SSH. Sessions expire and do not survive server restart.", s.OpenPodTerminal)
	terminalTool(server, "write_pod_terminal", "Send input to your terminal; include newline to execute. Input may be control characters such as U+0003 for Ctrl+C (TTY required). Sent is not confirmation of remote process termination.", s.WritePodTerminal)
	terminalTool(server, "read_pod_terminal", "Read up to 64 KiB of binary-safe outputBase64 with a byte cursor; waitSeconds 0-5. lostBytes reports overwritten ring-buffer data. Streams merged; ANSI terminal sequences retained.", s.ReadPodTerminal)
	terminalTool(server, "close_pod_terminal", "Close your session and release its connection. Can close after Pod deletion; remote process termination is not guaranteed.", s.ClosePodTerminal)
	terminalTool(server, "download_pod_file", "Prepare a single-use downloadId and HTTP POST downloadUrl, valid for five minutes and bound to the authenticated user and Pod UID. Use curl with the same Bearer credential and JSON {downloadId:...}, saving the binary response locally. No file bytes in MCP. No fixed size limit or resume. Container requires tar. ConfigMap symlinks are followed. Relative URLs use the same service and gateway prefix as this MCP.", s.DownloadPodFile)
}
func terminalTool[In, Out any](server *sdk.Server, name, description string, call func(context.Context, In) (Out, error)) {
	registerTool(server, name, description, false, true, api.ShellTimeout, call)
}
