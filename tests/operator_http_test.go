package tests

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	contract "olixops-k8s-ctl/internal/application/api"
	catalogapp "olixops-k8s-ctl/internal/application/catalog"
	catalogdomain "olixops-k8s-ctl/internal/domain/catalog"
	"olixops-k8s-ctl/internal/infrastructure/catalogdb"
	"path/filepath"
	"strings"
	"sync"
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
	"olixops-k8s-ctl/internal/infrastructure/config"
	k8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/router"
)

// One backend supports upload, download and a persistent echo terminal. All files
// are in memory; no test command touches the host or a real cluster.
type operatorStreams struct{ *uploadBackend }

func (b operatorStreams) Stream(ctx context.Context, ns, pod, container string, argv []string, tty bool, in io.Reader, out, stderr io.Writer) error {
	if argv[0] == "tar" {
		b.Lock()
		data := append([]byte{}, b.files["/tmp/data.bin"]...)
		b.Unlock()
		w := tar.NewWriter(out)
		if e := w.WriteHeader(&tar.Header{Name: "./data.bin", Mode: 0600, Size: int64(len(data))}); e != nil {
			return e
		}
		if _, e := w.Write(data); e != nil {
			return e
		}
		return w.Close()
	}
	if len(argv) == 1 && argv[0] == "/bin/sh" {
		_, e := io.Copy(out, in)
		return e
	}
	return b.uploadBackend.Stream(ctx, ns, pod, container, argv, tty, in, out, stderr)
}

type operatorEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func operatorRequest(t *testing.T, server, token, method, path string, body any) (int, operatorEnvelope) {
	t.Helper()
	var r io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			r = strings.NewReader(b)
		default:
			raw, e := json.Marshal(b)
			require.NoError(t, e)
			r = bytes.NewReader(raw)
		}
	}
	req, e := http.NewRequest(method, server+path, r)
	require.NoError(t, e)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "operator-test")
	req.Header.Set("X-User-Id", "forged-user")
	res, e := http.DefaultClient.Do(req)
	require.NoError(t, e)
	defer res.Body.Close()
	require.Equal(t, "operator-test", res.Header.Get("X-Request-Id"))
	var out operatorEnvelope
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return res.StatusCode, out
}
func operatorPodAPI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/log") {
			fmt.Fprint(w, "operator log\n")
			return
		}
		if strings.Contains(r.URL.Path, "/pods/") {
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"app","namespace":"demo","uid":"uid-1","labels":{"k8s-ctl/exec-enabled":"true"}},"spec":{"containers":[{"name":"app"}]},"status":{"containerStatuses":[{"name":"app","state":{"running":{}}}]}}`)
			return
		}
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"NamespaceList","items":[{"metadata":{"name":"demo"}}]}`)
	}))
}
func operatorClient(t *testing.T, cfg config.KubernetesConfig, endpoint string) *k8s.Client {
	t.Helper()
	p := memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: test\n  context:\n    cluster: test\n    user: test\ncurrent-context: test\nusers:\n- name: test\n  user: {}\n", endpoint))
	c, e := k8s.New(cfg, p)
	require.NoError(t, e)
	return c
}
func TestOperatorHTTPWithoutMainMCP(t *testing.T) {
	f := newAuthFixture(t)
	f.cfg.MCP.Enabled = false
	f.cfg.MCP.WriteEnabled = false
	f.cfg.Kubernetes = config.KubernetesConfig{InCluster: true, WriteEnabled: true, AllowedNamespaces: []string{"demo"}}
	f.cfg.Cluster.ID = "poc"
	f.cfg.Shell.Enabled = true
	f.cfg.PodExec = config.PodExecConfig{Enabled: true, AllowedNamespaces: []string{"demo"}}
	upstream := operatorPodAPI(t)
	defer upstream.Close()
	manager := sessions.NewManager(time.Minute, time.Hour, 4, 2)
	defer manager.Close()
	streams := operatorStreams{&uploadBackend{stages: map[string][]byte{}, files: map[string][]byte{}}}
	executor := &countingExecutor{}
	service := application.NewClusterService(operatorClient(t, f.cfg.Kubernetes, upstream.URL), nil, f.cfg.Kubernetes, application.WithPodExecutor(executor, []string{"demo"}), application.WithShell(true, true, interceptor.AllowAll{}), application.WithTerminals(streams, manager), application.WithClusterID("poc"))
	engine, e := router.NewWithAuth(f.cfg, service, nil, f.service)
	require.NoError(t, e)
	server := httptest.NewServer(engine)
	defer server.Close()
	token := f.company(t, nil, nil)
	call := func(path string, in any) (int, operatorEnvelope) {
		return operatorRequest(t, server.URL, token, "POST", path, in)
	}
	status, cap := operatorRequest(t, server.URL, token, "GET", "/api/v1/operator/capabilities", nil)
	require.Equal(t, 200, status)
	require.Contains(t, string(cap.Data), `"clusterId":"poc"`)
	require.Contains(t, string(cap.Data), `"mcp":false`)
	require.Contains(t, string(cap.Data), `"files":true`)
	status, _ = call("/api/v1/mcp", `{}`)
	require.Equal(t, 404, status)
	status, _ = operatorRequest(t, server.URL, token, "GET", "/api/v1/cluster/namespaces", nil)
	require.Equal(t, 200, status)
	for _, badToken := range []string{"invalid", f.company(t, func(c jwt.MapClaims) { c["aud"] = "wrong" }, nil)} {
		status, denied := operatorRequest(t, server.URL, badToken, "GET", "/api/v1/operator/capabilities", nil)
		require.Equal(t, 401, status)
		require.JSONEq(t, `{"reason":"unauthenticated"}`, string(denied.Data))
	}
	// The previous HTTP 8 KiB parser prevented commands accepted by the shared use case.
	command := domain.PodExecInput{Namespace: "demo", Pod: "app", Container: "app", Action: "shell", Command: strings.Repeat("x", 16<<10)}
	status, _ = call("/api/v1/cluster/pods/exec", command)
	require.Equal(t, 200, status)
	require.Equal(t, int32(1), executor.calls.Load())
	target := domain.TerminalTarget{Namespace: "demo", Pod: "app", Container: "app"}
	status, opened := call("/api/v1/cluster/pods/terminals/open", domain.TerminalOpenInput{TerminalTarget: target, TTY: true})
	require.Equal(t, 200, status, string(opened.Data))
	var term domain.TerminalResult
	require.NoError(t, json.Unmarshal(opened.Data, &term))
	other := f.company(t, func(c jwt.MapClaims) { c["staffuserid"] = "other" }, nil)
	status, _ = operatorRequest(t, server.URL, other, "POST", "/api/v1/cluster/pods/terminals/read", domain.TerminalReadInput{SessionID: term.SessionID})
	require.Equal(t, 404, status)
	status, _ = call("/api/v1/cluster/pods/terminals/write", domain.TerminalWriteInput{SessionID: term.SessionID, Input: "hello\n\x03"})
	require.Equal(t, 200, status)
	status, read := call("/api/v1/cluster/pods/terminals/read", domain.TerminalReadInput{SessionID: term.SessionID, WaitSeconds: 1})
	require.Equal(t, 200, status)
	var output domain.TerminalResult
	require.NoError(t, json.Unmarshal(read.Data, &output))
	data, e := base64.StdEncoding.DecodeString(output.OutputBase64)
	require.NoError(t, e)
	require.Equal(t, "hello\n\x03", string(data))
	status, _ = call("/api/v1/cluster/pods/terminals/close", domain.TerminalSessionInput{SessionID: term.SessionID})
	require.Equal(t, 200, status)
	// Simulate the upper platform streaming through a prefix, preserving Content-Length.
	u, e := url.Parse(server.URL)
	require.NoError(t, e)
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.FlushInterval = -1
	var seenLength int64
	var mu sync.Mutex
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenLength = r.ContentLength
		mu.Unlock()
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/k8sctl")
		proxy.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	payload := bytes.Repeat([]byte{0, 255, 128, 10}, (1<<18)+1)
	status, prepared := call("/api/v1/cluster/pods/files/prepare-upload", domain.UploadInput{TerminalTarget: target, Path: "/tmp/data.bin"})
	require.Equal(t, 200, status)
	var ticket domain.UploadOutput
	require.NoError(t, json.Unmarshal(prepared.Data, &ticket))
	req, e := http.NewRequest("POST", gateway.URL+"/k8sctl"+ticket.UploadURL, bytes.NewReader(payload))
	require.NoError(t, e)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Upload-Id", ticket.UploadID)
	res, e := http.DefaultClient.Do(req)
	require.NoError(t, e)
	raw, e := io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, e)
	require.Equal(t, 200, res.StatusCode, string(raw))
	mu.Lock()
	require.Equal(t, int64(len(payload)), seenLength)
	mu.Unlock()
	require.Contains(t, string(raw), fmt.Sprintf("%x", sha256.Sum256(payload)))
	status, prepared = call("/api/v1/cluster/pods/files/prepare-download", domain.DownloadInput{TerminalTarget: target, Path: "/tmp/data.bin"})
	require.Equal(t, 200, status)
	var download domain.DownloadOutput
	require.NoError(t, json.Unmarshal(prepared.Data, &download))
	raw, _ = json.Marshal(domain.DownloadRequest{DownloadID: download.DownloadID})
	req, e = http.NewRequest("POST", gateway.URL+"/k8sctl"+download.DownloadURL, bytes.NewReader(raw))
	require.NoError(t, e)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, e = http.DefaultClient.Do(req)
	require.NoError(t, e)
	data, e = io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, e)
	require.Equal(t, 200, res.StatusCode)
	require.Equal(t, payload, data)
	// Reusing a consumed reference is a machine-readable not_found, not a success.
	status, _ = call(download.DownloadURL, domain.DownloadRequest{DownloadID: download.DownloadID})
	require.Equal(t, 404, status)
}

func TestHTTPMCPInputAndResultParity(t *testing.T) {
	f := newAuthFixture(t)
	f.cfg.MCP.WriteEnabled = true
	upstream := operatorPodAPI(t)
	defer upstream.Close()
	service := application.NewClusterService(operatorClient(t, f.cfg.Kubernetes, upstream.URL), nil, f.cfg.Kubernetes)
	engine, e := router.NewWithAuth(f.cfg, service, nil, f.service)
	require.NoError(t, e)
	server := httptest.NewServer(engine)
	defer server.Close()
	token := f.company(t, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, e := sdk.NewClient(&sdk.Implementation{Name: "parity", Version: "1"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token}}}, nil)
	require.NoError(t, e)
	defer client.Close()
	status, rest := operatorRequest(t, server.URL, token, "GET", "/api/v1/cluster/namespaces", nil)
	require.Equal(t, 200, status)
	out, e := client.CallTool(ctx, &sdk.CallToolParams{Name: "list_namespaces", Arguments: map[string]any{}})
	require.NoError(t, e)
	require.False(t, out.IsError)
	raw, e := json.Marshal(out.StructuredContent)
	require.NoError(t, e)
	require.JSONEq(t, string(rest.Data), string(raw))
	for _, in := range []map[string]any{
		{"namespace": "demo", "kind": "Deployment", "name": "app"},
		{"namespace": "demo", "kind": "Deployment", "name": "app", "replicas": 0, "extra": true},
		{"namespace": "demo", "kind": "Deployment", "name": "app", "replicas": -1},
		{"namespace": "demo", "kind": "Deployment", "name": "app", "replicas": nil},
	} {
		status, _ = operatorRequest(t, server.URL, token, "POST", "/api/v1/cluster/workloads/scale", in)
		require.NotEqual(t, 200, status)
		out, e = client.CallTool(ctx, &sdk.CallToolParams{Name: "scale_workload", Arguments: in})
		require.True(t, e != nil || out.IsError)
	}
	status, rest = operatorRequest(t, server.URL, token, "POST", "/api/v1/cluster/namespaces/metadata", `{"namespace":"demo","spec":{}}`)
	require.Equal(t, 400, status)
	require.Contains(t, string(rest.Data), "invalid_argument")
	// A zero replica count is valid; it reaches the disabled-write check rather than missing-field validation.
	status, rest = operatorRequest(t, server.URL, token, "POST", "/api/v1/cluster/workloads/scale", `{"namespace":"demo","kind":"Deployment","name":"app","replicas":0}`)
	require.Equal(t, 403, status)
	require.Contains(t, string(rest.Data), "feature_disabled")
}

func TestAllToolsHaveHTTPRoutes(t *testing.T) {
	f := newAuthFixture(t)
	f.cfg.MCP.WriteEnabled = true
	f.cfg.Kubernetes.WriteEnabled = true
	f.cfg.Kubernetes.InCluster = true
	f.cfg.Shell.Enabled = true
	f.cfg.PodExec = config.PodExecConfig{Enabled: true, AllowedNamespaces: []string{"demo"}}
	store, e := catalogdb.Open(filepath.Join(t.TempDir(), "catalog.db"))
	require.NoError(t, e)
	defer store.Close()
	cat, e := catalogapp.New(context.Background(), store, catalogReadCluster{}, catalogdomain.Gateway{ID: "higress", Cluster: "test", Namespace: "demo", Name: "gateway", Service: "gateway", PortStart: 30000, PortEnd: 30001}, nil)
	require.NoError(t, e)
	svc := application.NewClusterService(nil, nil, f.cfg.Kubernetes)
	engine, e := router.NewWithAuth(f.cfg, svc, cat, f.service)
	require.NoError(t, e)
	paths := map[string]bool{}
	for _, r := range engine.Routes() {
		paths[r.Method+" "+r.Path] = true
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, e := sdk.NewClient(&sdk.Implementation{Name: "coverage", Version: "1"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport{f.company(t, nil, nil)}}}, nil)
	require.NoError(t, e)
	defer session.Close()
	all := map[string]bool{}
	cursor := ""
	for {
		list, e := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		require.NoError(t, e)
		for _, tool := range list.Tools {
			all[tool.Name] = true
		}
		cursor = list.NextCursor
		if cursor == "" {
			break
		}
	}
	require.Len(t, all, len(contract.Routes))
	for _, r := range contract.Routes {
		require.True(t, all[r.Tool], r.Tool)
		require.True(t, paths[r.Method+" "+r.Path], r.Path)
		delete(all, r.Tool)
	}
	require.Empty(t, all)
	// HTTP-off still serves the file stream needed by directly connected MCP clients.
	off := false
	f.cfg.HTTP.Enabled = &off
	onlyMCP, e := router.NewWithAuth(f.cfg, svc, cat, f.service)
	require.NoError(t, e)
	paths = map[string]bool{}
	for _, r := range onlyMCP.Routes() {
		paths[r.Method+" "+r.Path] = true
	}
	require.False(t, paths["GET /api/v1/cluster/status"])
	require.False(t, paths["POST /api/v1/cluster/pods/terminals/open"])
	require.True(t, paths["POST "+application.DownloadRoute])
	require.True(t, paths["POST "+application.UploadRoute])
	require.True(t, paths["GET /api/v1/pub/health"])
	require.True(t, paths["GET /api/v1/auth/me"])
}

type cancelDownloadBackend struct {
	done    chan struct{}
	written atomic.Int64
}

func (b *cancelDownloadBackend) Stream(ctx context.Context, _, _, _ string, argv []string, _ bool, _ io.Reader, out, stderr io.Writer) error {
	defer close(b.done)
	if argv[0] != "tar" {
		return fmt.Errorf("unexpected command")
	}
	const size = 64 << 20
	tw := tar.NewWriter(out)
	if err := tw.WriteHeader(&tar.Header{Name: "./large.bin", Mode: 0600, Size: size}); err != nil {
		return err
	}
	chunk := make([]byte, 64<<10)
	for b.written.Load() < size {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := tw.Write(chunk)
		b.written.Add(int64(n))
		if err != nil {
			return err
		}
	}
	return tw.Close()
}
func TestOperatorProxyDownloadCancellation(t *testing.T) {
	f := newAuthFixture(t)
	f.cfg.MCP.Enabled = false
	f.cfg.Kubernetes = config.KubernetesConfig{InCluster: true, WriteEnabled: true, AllowedNamespaces: []string{"demo"}}
	f.cfg.Shell.Enabled = true
	f.cfg.PodExec = config.PodExecConfig{Enabled: true, AllowedNamespaces: []string{"demo"}}
	upstream := operatorPodAPI(t)
	defer upstream.Close()
	manager := sessions.NewManager(time.Minute, time.Hour, 2, 1)
	defer manager.Close()
	backend := &cancelDownloadBackend{done: make(chan struct{})}
	svc := application.NewClusterService(operatorClient(t, f.cfg.Kubernetes, upstream.URL), nil, f.cfg.Kubernetes, application.WithPodExecutor(&countingExecutor{}, []string{"demo"}), application.WithShell(true, true, interceptor.AllowAll{}), application.WithTerminals(backend, manager))
	engine, e := router.NewWithAuth(f.cfg, svc, nil, f.service)
	require.NoError(t, e)
	server := httptest.NewServer(engine)
	defer server.Close()
	target, _ := url.Parse(server.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	upper := httptest.NewServer(proxy)
	defer upper.Close()
	token := f.company(t, nil, nil)
	status, prepared := operatorRequest(t, upper.URL, token, "POST", "/api/v1/cluster/pods/files/prepare-download", domain.DownloadInput{TerminalTarget: domain.TerminalTarget{Namespace: "demo", Pod: "app", Container: "app"}, Path: "/tmp/large.bin"})
	require.Equal(t, 200, status)
	var ticket domain.DownloadOutput
	require.NoError(t, json.Unmarshal(prepared.Data, &ticket))
	data, _ := json.Marshal(domain.DownloadRequest{DownloadID: ticket.DownloadID})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, e := http.NewRequestWithContext(ctx, "POST", upper.URL+ticket.DownloadURL, bytes.NewReader(data))
	require.NoError(t, e)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, e := http.DefaultClient.Do(request)
	require.NoError(t, e)
	require.Equal(t, 200, response.StatusCode)
	buf := make([]byte, 1024)
	_, e = io.ReadFull(response.Body, buf)
	require.NoError(t, e)
	require.Less(t, backend.written.Load(), int64(64<<20), "proxy must deliver data before buffering the entire file")
	cancel()
	response.Body.Close()
	select {
	case <-backend.done:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream producer did not stop after client cancellation")
	}
	require.Less(t, backend.written.Load(), int64(64<<20))
}
