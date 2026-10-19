package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/router"
)

type memoryKubeconfig []byte

func (p memoryKubeconfig) Load(context.Context, kubeconfig.Reference) (kubeconfig.Document, error) {
	return kubeconfig.Document{Bytes: p, Origin: "test"}, nil
}

// Real SDK client -> Gin -> MCP -> application -> Kubernetes HTTP adapter.
// The fake API rejects all writes, and no kubectl/Helm runner is supplied.
func TestMCPReadOnlyHTTP(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		if r.Method != http.MethodGet {
			t.Errorf("unexpected Kubernetes write: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"major":"1","minor":"28","gitVersion":"v1.28.15"}`)
		case "/api/v1/namespaces":
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"NamespaceList","items":[{"metadata":{"name":"demo"},"status":{"phase":"Active"}},{"metadata":{"name":"private"}}]}`)
		case "/apis/storage.k8s.io/v1/storageclasses":
			fmt.Fprint(w, `{"apiVersion":"storage.k8s.io/v1","kind":"StorageClassList","items":[{"metadata":{"name":"standard"},"provisioner":"test"}]}`)
		case "/apis/apps/v1":
			fmt.Fprint(w, `{"apiVersion":"v1","groupVersion":"apps/v1","resources":[{"name":"deployments","kind":"Deployment","namespaced":true},{"name":"statefulsets","kind":"StatefulSet","namespaced":true}]}`)
		case "/apis/apps/v1/namespaces/demo/deployments/nginx-demo":
			fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"nginx-demo","generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"replicas":1,"readyReplicas":1,"updatedReplicas":1,"availableReplicas":1}}`)
		case "/apis/apps/v1/namespaces/demo/deployments/broken":
			w.WriteHeader(403)
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","reason":"Forbidden","message":"private-upstream-address-token","code":403}`)
		case "/apis/apps/v1/namespaces/demo/statefulsets/db":
			fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"StatefulSet","metadata":{"name":"db","generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"replicas":1,"readyReplicas":1,"updatedReplicas":1,"currentRevision":"a","updateRevision":"a"}}`)
		case "/apis/gateway.networking.k8s.io/v1/httproutes":
			t.Errorf("HTTPRoute must be namespaced")
			w.WriteHeader(400)
		case "/apis/gateway.networking.k8s.io/v1/namespaces/demo/httproutes":
			w.WriteHeader(404)
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","reason":"NotFound","code":404}`)
		case "/apis/gateway.networking.k8s.io/v1beta1/namespaces/demo/httproutes":
			fmt.Fprint(w, `{"apiVersion":"gateway.networking.k8s.io/v1","kind":"HTTPRouteList","items":[{"metadata":{"name":"web","namespace":"demo"}}]}`)
		case "/apis/gateway.networking.k8s.io/v1alpha2/namespaces/demo/tcproutes", "/apis/gateway.networking.k8s.io/v1/namespaces/demo/tcproutes", "/apis/gateway.networking.k8s.io/v1beta1/namespaces/demo/tcproutes":
			w.WriteHeader(404)
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","reason":"NotFound","code":404}`)
		default:
			// All remaining known list endpoints return an empty list.
			if strings.Contains(r.URL.Path, "secret") || strings.Contains(r.URL.Path, "private") {
				t.Errorf("out-of-scope request: %s", r.URL.Path)
			}
			fmt.Fprint(w, `{"apiVersion":"v1","kind":"List","items":[]}`)
		}
	}))
	defer upstream.Close()
	cfg := &config.Config{Server: config.ServerConfig{Mode: "test"}, App: config.AppConfig{Version: "test"}, MCP: config.MCPConfig{Enabled: true}, Kubernetes: config.KubernetesConfig{AllowedNamespaces: []string{"demo"}}, Control: config.ControlConfig{APIKey: "rest-only-key"}}
	provider := memoryKubeconfig(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user: {}
`, upstream.URL))
	client, err := clusterk8s.New(cfg.Kubernetes, provider)
	require.NoError(t, err)
	service := application.NewClusterService(client, nil, cfg.Kubernetes)
	engine, err := router.New(cfg, service)
	require.NoError(t, err)
	server := httptest.NewServer(engine)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sdkClient := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := sdkClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/api/v1/pub/mcp"}, nil)
	require.NoError(t, err)
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 16)
	expected := map[string]bool{"cluster_status": true, "list_namespaces": true, "list_storage_classes": true, "list_resources": true, "list_gateway_resources": true, "get_rollout_status": true, "validate_manifest": true, "get_resource": true, "get_resource_yaml": true, "list_resource_objects": true, "list_events": true, "get_pod_logs": true, "list_helm_releases": true, "get_helm_status": true, "get_helm_history": true, "get_workload_history": true}
	for _, tool := range tools.Tools {
		require.True(t, expected[tool.Name], tool.Name)
		require.NotNil(t, tool.Annotations)
		require.True(t, tool.Annotations.ReadOnlyHint)
	}
	call := func(name string, args map[string]any, fail bool) string {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		require.Equal(t, fail, result.IsError)
		data, err := json.Marshal(result)
		require.NoError(t, err)
		return string(data)
	}
	require.Contains(t, call("cluster_status", map[string]any{}, false), `"namespaceCount":1`)
	require.NotContains(t, call("list_namespaces", map[string]any{}, false), "private")
	require.Contains(t, call("list_storage_classes", map[string]any{}, false), "standard")
	call("list_resources", map[string]any{"namespace": "demo"}, false)
	yamlView := call("get_resource_yaml", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "nginx-demo"}, false)
	require.Contains(t, yamlView, "apiVersion: apps/v1")
	require.Contains(t, yamlView, "kind: Deployment")
	call("get_resource_yaml", map[string]any{"namespace": "demo", "kind": "Secret", "name": "credentials"}, true)
	gateway := call("list_gateway_resources", map[string]any{"namespace": "demo"}, false)
	require.Contains(t, gateway, "HTTPRoute")
	require.Contains(t, gateway, `"missingResources":["TCPRoute"]`)
	require.Contains(t, call("get_rollout_status", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "nginx-demo"}, false), `"complete":true`)
	require.Contains(t, call("get_rollout_status", map[string]any{"namespace": "demo", "kind": "StatefulSet", "name": "db"}, false), `"complete":false`)
	require.NotContains(t, call("get_rollout_status", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "broken"}, true), "private-upstream-address-token")
	count := func() int { mu.Lock(); defer mu.Unlock(); return requests }
	before := count()
	call("list_resources", map[string]any{"namespace": "private"}, true)
	call("list_gateway_resources", map[string]any{"namespace": "private"}, true)
	call("get_rollout_status", map[string]any{"namespace": "demo", "kind": "Secret", "name": "x"}, true)
	call("get_rollout_status", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "../../private"}, true)
	call("list_resources", map[string]any{}, true)
	manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n"
	require.Contains(t, call("validate_manifest", map[string]any{"namespace": "demo", "manifest": manifest}, false), `"valid":true`)
	call("validate_manifest", map[string]any{"namespace": "private", "manifest": manifest}, true)
	call("validate_manifest", map[string]any{"namespace": "demo", "manifest": strings.ReplaceAll(manifest, "ConfigMap", "Secret")}, true)
	sensitive := call("validate_manifest", map[string]any{"namespace": "demo", "manifest": "sensitive-value: [broken"}, true)
	require.NotContains(t, sensitive, "sensitive-value")
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "apply_manifest", Arguments: map[string]any{}})
	require.Error(t, err)
	require.Equal(t, before, count(), "invalid inputs and local validation must never contact Kubernetes")

	reqIDRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/pub/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":42,"method":"tools/list","params":{}}`))
	require.NoError(t, err)
	reqIDRequest.Header.Set("Content-Type", "application/json")
	reqIDRequest.Header.Set("Accept", "application/json, text/event-stream")
	reqIDRequest.Header.Set("MCP-Protocol-Version", "2025-11-25")
	reqIDRequest.Header.Set("X-Request-Id", "mcp-test-request")
	reqIDResponse, err := http.DefaultClient.Do(reqIDRequest)
	require.NoError(t, err)
	reqIDResponse.Body.Close()
	require.Equal(t, http.StatusOK, reqIDResponse.StatusCode)
	require.Equal(t, "mcp-test-request", reqIDResponse.Header.Get("X-Request-Id"))

	// Anonymous REST writes are still governed by the existing REST key middleware.
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/cluster/kubectl/apply", strings.NewReader(`{}`))
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)

	req, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/pub/mcp", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Origin", "https://untrusted.example")
	req.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusForbidden, response.StatusCode)

	cfg.MCP.Enabled = false
	disabled, err := router.New(cfg, service)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	disabled.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/pub/mcp", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMCPConfigEnvironment(t *testing.T) {
	t.Setenv("SERVER_HOST", "127.0.0.1")
	t.Setenv("MCP_WRITE_ENABLED", "true")
	t.Setenv("MCP_ENABLED", "false")
	cfg, err := config.Load("../configs")
	require.NoError(t, err)
	require.False(t, cfg.MCP.Enabled)
	require.True(t, cfg.MCP.WriteEnabled)
	t.Setenv("MCP_WRITE_ENABLED", "false")
	t.Setenv("MCP_ENABLED", "true")
	cfg, err = config.Load("../configs")
	require.NoError(t, err)
	require.True(t, cfg.MCP.Enabled)
	require.False(t, cfg.MCP.WriteEnabled)
}
