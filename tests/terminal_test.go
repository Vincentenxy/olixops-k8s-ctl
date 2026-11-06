package tests

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"olixops-k8s-ctl/internal/domain"
	authdomain "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/internal/infrastructure/config"
	k8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/router"
)

type terminalBackend struct {
	file      []byte
	fail      bool
	downloads atomic.Int32
}

func (b *terminalBackend) Stream(ctx context.Context, ns, pod, container string, argv []string, tty bool, in io.Reader, out, stderr io.Writer) error {
	if argv[0] == "/bin/sh" {
		_, err := io.Copy(out, in)
		return err
	}
	b.downloads.Add(1)
	if argv[0] != "tar" || argv[len(argv)-1] != "./data.bin" {
		return fmt.Errorf("wrong download command")
	}
	writer := tar.NewWriter(out)
	if err := writer.WriteHeader(&tar.Header{Name: "./data.bin", Mode: 0600, Size: int64(len(b.file))}); err != nil {
		return err
	}
	if _, err := writer.Write(b.file); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if b.fail {
		return fmt.Errorf("remote exec failed after output")
	}
	return nil
}
func TestTerminalAndBinaryDownloadThroughMainMCP(t *testing.T) {
	f := newAuthFixture(t)
	var podUID atomic.Value
	podUID.Store("uid-1")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"app","namespace":"demo","uid":%q,"labels":{"k8s-ctl/exec-enabled":"true"}},"spec":{"containers":[{"name":"app"}]},"status":{"containerStatuses":[{"name":"app","state":{"running":{}}}]}}`, podUID.Load())
	}))
	defer api.Close()
	provider := memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: test\n  context:\n    cluster: test\n    user: test\ncurrent-context: test\nusers:\n- name: test\n  user: {}\n", api.URL))
	f.cfg.Kubernetes.InCluster = true
	f.cfg.Kubernetes.WriteEnabled = true
	f.cfg.Kubernetes.AllowedNamespaces = []string{"demo"}
	f.cfg.MCP.WriteEnabled = true
	f.cfg.Shell.Enabled = true
	f.cfg.PodExec = config.PodExecConfig{Enabled: true, AllowedNamespaces: []string{"demo"}}
	client, err := k8s.New(f.cfg.Kubernetes, provider)
	require.NoError(t, err)
	manager := sessions.NewManager(time.Minute, time.Hour, 4, 2)
	defer manager.Close()
	backend := &terminalBackend{file: []byte{0, 1, 255, 128, 10, 0}}
	service := application.NewClusterService(client, nil, f.cfg.Kubernetes, application.WithPodExecutor(&countingExecutor{}, []string{"demo"}), application.WithShell(true, true, interceptor.AllowAll{}), application.WithTerminals(backend, manager))
	engine, err := router.NewWithAuth(f.cfg, service, nil, f.service)
	require.NoError(t, err)
	httpServer := httptest.NewServer(engine)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "terminal-test", Version: "1"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: httpServer.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport{f.company(t, nil, nil)}}}, nil)
	require.NoError(t, err)
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"cluster_status", "exec_pod_command", "open_pod_terminal", "write_pod_terminal", "read_pod_terminal", "close_pod_terminal", "download_pod_file"} {
		require.True(t, names[name], name)
	}
	call := func(name string, args any) *sdk.CallToolResult {
		out, e := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, e)
		return out
	}
	target := domain.TerminalTarget{Namespace: "demo", Pod: "app", Container: "app"}
	opened := call("open_pod_terminal", domain.TerminalOpenInput{TerminalTarget: target, TTY: true})
	require.False(t, opened.IsError)
	// Direct application calls verify identity isolation independently of the MCP connection.
	alice := authdomain.WithPrincipal(ctx, authdomain.Principal{UserID: "alice"})
	bob := authdomain.WithPrincipal(ctx, authdomain.Principal{UserID: "bob"})
	terminal, err := service.OpenPodTerminal(alice, domain.TerminalOpenInput{TerminalTarget: target})
	require.NoError(t, err)
	_, err = service.WritePodTerminal(bob, domain.TerminalWriteInput{SessionID: terminal.SessionID, Input: "id\n"})
	require.Error(t, err)
	_, err = service.ReadPodTerminal(bob, domain.TerminalReadInput{SessionID: terminal.SessionID})
	require.Error(t, err)
	_, err = service.ClosePodTerminal(bob, domain.TerminalSessionInput{SessionID: terminal.SessionID})
	require.Error(t, err)
	_, err = service.WritePodTerminal(alice, domain.TerminalWriteInput{SessionID: terminal.SessionID, Input: "pwd\n"})
	require.NoError(t, err)
	read, err := service.ReadPodTerminal(alice, domain.TerminalReadInput{SessionID: terminal.SessionID, WaitSeconds: 1})
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(read.OutputBase64)
	require.NoError(t, err)
	require.Equal(t, "pwd\n", string(decoded))
	deny := &shellTestPolicy{deny: true}
	application.WithShell(true, true, deny)(service)
	rejected := call("download_pod_file", domain.DownloadInput{TerminalTarget: target, Path: "/app/data.bin"})
	require.True(t, rejected.IsError)
	require.Zero(t, backend.downloads.Load())
	_, err = service.WritePodTerminal(alice, domain.TerminalWriteInput{SessionID: terminal.SessionID, Input: "id\n"})
	require.Error(t, err)
	application.WithShell(true, true, interceptor.AllowAll{})(service)
	// Prepared references carry no file bytes; authenticated HTTP streams the payload.
	download := func(ticket domain.DownloadOutput, authToken string, rangeHeader string) (*http.Response, error) {
		payload, _ := json.Marshal(domain.DownloadRequest{DownloadID: ticket.DownloadID})
		request, _ := http.NewRequest("POST", httpServer.URL+application.DownloadRoute, bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			request.Header.Set("Authorization", "Bearer "+authToken)
		}
		if rangeHeader != "" {
			request.Header.Set("Range", rangeHeader)
		}
		return http.DefaultClient.Do(request)
	}
	prepared := call("download_pod_file", domain.DownloadInput{TerminalTarget: target, Path: "/app/data.bin"})
	require.False(t, prepared.IsError)
	raw, err := json.Marshal(prepared.StructuredContent)
	require.NoError(t, err)
	var ticket domain.DownloadOutput
	require.NoError(t, json.Unmarshal(raw, &ticket))
	require.Equal(t, "POST", ticket.Method)
	response, err := download(ticket, "", "")
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, 401, response.StatusCode)
	response, err = download(ticket, f.company(t, func(c jwt.MapClaims) { c["staffuserid"] = "bob" }, nil), "")
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, 404, response.StatusCode)
	response, err = download(ticket, f.company(t, nil, nil), "bytes=1-")
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, 400, response.StatusCode)
	response, err = download(ticket, f.company(t, nil, nil), "")
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	decoded, err = io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, backend.file, decoded)
	require.Contains(t, response.Header.Get("Content-Disposition"), "data.bin")
	response, err = download(ticket, f.company(t, nil, nil), "")
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, 404, response.StatusCode)
	backend.file = bytes.Repeat([]byte{1}, (1<<20)+1)
	prepared = call("download_pod_file", domain.DownloadInput{TerminalTarget: target, Path: "/app/data.bin"})
	require.False(t, prepared.IsError)
	raw, err = json.Marshal(prepared.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &ticket))
	response, err = download(ticket, f.company(t, nil, nil), "")
	require.NoError(t, err)
	decoded, err = io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, backend.file, decoded)
	// A late exec failure must break HTTP framing even if all payload bytes arrived.
	backend.fail = true
	late, err := service.DownloadPodFile(authdomain.WithPrincipal(ctx, authdomain.Principal{UserID: "wx-user"}), domain.DownloadInput{TerminalTarget: target, Path: "/app/data.bin"})
	require.NoError(t, err)
	response, err = download(late, f.company(t, nil, nil), "")
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	response.Body.Close()
	require.Error(t, err, "incomplete transfer must not look successful")
	backend.fail = false
	backend.file = nil
	empty, err := service.DownloadPodFile(authdomain.WithPrincipal(ctx, authdomain.Principal{UserID: "wx-user"}), domain.DownloadInput{TerminalTarget: target, Path: "/app/data.bin"})
	require.NoError(t, err)
	response, err = download(empty, f.company(t, nil, nil), "")
	require.NoError(t, err)
	decoded, err = io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Empty(t, decoded)
	_, err = service.DownloadPodFile(alice, domain.DownloadInput{TerminalTarget: target, Path: "/proc/1/environ"})
	require.Error(t, err)
	replaced, err := service.DownloadPodFile(authdomain.WithPrincipal(ctx, authdomain.Principal{UserID: "wx-user"}), domain.DownloadInput{TerminalTarget: target, Path: "/app/data.bin"})
	require.NoError(t, err)
	before := backend.downloads.Load()
	podUID.Store("uid-2")
	response, err = download(replaced, f.company(t, nil, nil), "")
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, 409, response.StatusCode)
	require.Equal(t, before, backend.downloads.Load())
	_, err = service.WritePodTerminal(alice, domain.TerminalWriteInput{SessionID: terminal.SessionID, Input: "id\n"})
	require.ErrorContains(t, err, "replaced")
	_, err = manager.Inspect("alice", terminal.SessionID)
	require.Error(t, err)
}
