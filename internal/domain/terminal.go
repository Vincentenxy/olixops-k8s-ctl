package domain

import (
	"context"
	"io"
)

// PodStreamBackend uses the same Provider-backed client as target inspection.
type PodStreamBackend interface {
	Stream(context.Context, string, string, string, []string, bool, io.Reader, io.Writer, io.Writer) error
}
type TerminalTarget struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
}
type TerminalOpenInput struct {
	TerminalTarget
	TTY bool `json:"tty,omitempty"`
}
type TerminalSessionInput struct {
	SessionID string `json:"sessionId"`
}
type TerminalWriteInput struct {
	SessionID string `json:"sessionId"`
	Input     string `json:"input"`
}
type TerminalReadInput struct {
	SessionID   string `json:"sessionId"`
	Cursor      int64  `json:"cursor,omitempty"`
	WaitSeconds int    `json:"waitSeconds,omitempty"`
}
type TerminalResult struct {
	SessionID           string `json:"sessionId"`
	State               string `json:"state"`
	OutputBase64        string `json:"outputBase64,omitempty"`
	NextCursor          int64  `json:"nextCursor"`
	LostBytes           int64  `json:"lostBytes"`
	RemoteExitConfirmed bool   `json:"remoteExitConfirmed"`
}
type DownloadInput struct {
	TerminalTarget
	Path string `json:"path"`
}
type DownloadOutput struct {
	DownloadID  string `json:"downloadId"`
	DownloadURL string `json:"downloadUrl"`
	Method      string `json:"method"`
	Name        string `json:"name"`
	ExpiresAt   string `json:"expiresAt"`
	PodUID      string `json:"podUID"`
}
type DownloadRequest struct {
	DownloadID string `json:"downloadId"`
}

type UploadInput struct {
	TerminalTarget
	Path string `json:"path"`
}
type UploadOutput struct {
	UploadID  string `json:"uploadId"`
	UploadURL string `json:"uploadUrl"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	ExpiresAt string `json:"expiresAt"`
	PodUID    string `json:"podUID"`
}
type UploadResult struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	PodUID string `json:"podUID"`
}
