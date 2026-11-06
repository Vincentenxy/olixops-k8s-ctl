package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/domain"
	"olixops-k8s-ctl/internal/infrastructure/config"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/router"
)

func TestDiagnosticOnlyMCPAndRESTEnforcePerUserGrants(t *testing.T) {
	f := newAuthFixture(t)
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/log") {
			fmt.Fprint(w, "fixture log\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// No exec-enabled label: the exact UID grant explicitly opts this Pod in.
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Pod","metadata":{"namespace":"demo","name":"app","uid":"uid-1"},"spec":{"containers":[{"name":"app"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","state":{"running":{}}}]}}`)
	}))
	defer api.Close()
	provider := memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: test\n  context:\n    cluster: test\n    user: test\ncurrent-context: test\nusers:\n- name: test\n  user: {}\n", api.URL))
	f.cfg.MCP.DiagnosticsOnly = true
	f.cfg.MCP.WriteEnabled = true
	f.cfg.Kubernetes.WriteEnabled = true
	f.cfg.Kubernetes.AllowedNamespaces = []string{"demo"}
	f.cfg.PodExec = config.PodExecConfig{Enabled: true, Mode: "managed", Binary: "fixture-upstream", AllowedNamespaces: []string{"demo"}, Grants: []domain.DiagnosticGrant{{UserID: "wx-user", Namespace: "demo", Pod: "app", Container: "app", PodUID: "uid-1", Actions: []string{"logs", "environment"}}}}
	kube, err := clusterk8s.New(f.cfg.Kubernetes, provider)
	require.NoError(t, err)
	backend := &countingExecutor{}
	service := application.NewClusterService(kube, nil, f.cfg.Kubernetes, application.WithPodExecutor(backend, []string{"demo"}), application.WithDiagnosticGrants(f.cfg.PodExec.Grants))
	unguarded := application.NewClusterService(kube, nil, f.cfg.Kubernetes, application.WithPodExecutor(backend, []string{"demo"}))
	_, err = router.NewWithAuth(f.cfg, unguarded, nil, f.service)
	require.Error(t, err)
	engine, err := router.NewWithAuth(f.cfg, service, nil, f.service)
	require.NoError(t, err)
	raw := f.company(t, nil, nil)
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/v1/cluster/status", "/api/v1/services/standard", "/api/v1/ports"} {
		require.Equal(t, 404, request("GET", path, raw, "").Code)
	}
	require.Equal(t, 404, request("POST", "/api/v1/cluster/kubectl/apply", raw, `{}`).Code)
	body := `{"namespace":"demo","pod":"app","container":"app","action":"environment"}`
	require.Equal(t, 200, request("POST", "/api/v1/cluster/pods/exec", raw, body).Code)
	require.Equal(t, int32(1), backend.calls.Load())
	// A different verified user is denied before any Kubernetes query.
	other := f.company(t, func(c jwt.MapClaims) { c["staffuserid"] = "other-user" }, nil)
	before := calls.Load()
	require.Equal(t, 403, request("POST", "/api/v1/cluster/pods/exec", other, body).Code)
	require.Equal(t, 403, request("POST", "/api/v1/cluster/pods/logs", other, `{"namespace":"demo","pod":"app","container":"app"}`).Code)
	require.Equal(t, before, calls.Load())
	require.Equal(t, 403, request("POST", "/api/v1/cluster/pods/exec", raw, strings.Replace(body, "environment", "sh -c id", 1)).Code)
	server := httptest.NewServer(engine)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport{raw}}}, nil)
	require.NoError(t, err)
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := []string{}
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	require.ElementsMatch(t, []string{"get_pod_logs", "exec_pod_command"}, names)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_pod_logs", Arguments: map[string]any{"namespace": "demo", "pod": "app", "container": "app"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	var output map[string]any
	encoded, _ := json.Marshal(result.StructuredContent)
	require.NoError(t, json.Unmarshal(encoded, &output))
	require.Equal(t, "fixture log\n", output["text"])
}
