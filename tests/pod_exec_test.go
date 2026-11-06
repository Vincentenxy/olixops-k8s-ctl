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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application"
	authapp "olixops-k8s-ctl/internal/application/auth"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"olixops-k8s-ctl/internal/domain"
	authdomain "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/k8smcp"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/router"
)

type execPolicy struct {
	user string
	deny bool
}

func (p *execPolicy) Authorize(ctx context.Context, req domain.AuthorizationRequest) error {
	identity, _ := authdomain.FromContext(ctx)
	p.user = identity.UserID
	if req.Action != "pod.exec" || !req.Write || p.deny {
		return fmt.Errorf("policy rejected")
	}
	return nil
}
func TestAuthenticatedExecIntegration(t *testing.T) {
	f := newAuthFixture(t)
	var calls atomic.Int32
	expectedCommand := []any{"redis-cli", "PING"}
	upstreamServer := mcp.NewServer(&mcp.Implementation{Name: "upstream-fixture", Version: "v0.1.2"}, nil)
	upstreamServer.AddTool(&mcp.Tool{Name: "pods_exec", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		var args map[string]any
		require.NoError(t, json.Unmarshal(r.Params.Arguments, &args))
		require.Equal(t, expectedCommand, args["command"])
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "PONG"}}}, nil
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return upstreamServer }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer upstream.Close()
	backend, err := k8smcp.New(upstream.URL + "/mcp")
	require.NoError(t, err)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/namespaces/demo/pods/redis-0", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"redis-0","namespace":"demo","uid":"uid-123","labels":{"k8s-ctl/exec-enabled":"true"}},"spec":{"containers":[{"name":"redis"}]},"status":{"phase":"Running","containerStatuses":[{"name":"redis","state":{"running":{}}}]}}`)
	}))
	defer api.Close()
	provider := memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: test\n  context:\n    cluster: test\n    user: test\ncurrent-context: test\nusers:\n- name: test\n  user: {}\n", api.URL))
	f.cfg.MCP.WriteEnabled = true
	f.cfg.Kubernetes.WriteEnabled = true
	f.cfg.Kubernetes.InCluster = true
	f.cfg.Kubernetes.AllowedNamespaces = []string{"demo"}
	f.cfg.PodExec = config.PodExecConfig{Enabled: true, Endpoint: upstream.URL + "/mcp", AllowedNamespaces: []string{"demo"}}
	kube, err := clusterk8s.New(f.cfg.Kubernetes, provider)
	require.NoError(t, err)
	policy := &execPolicy{}
	service := application.NewClusterService(kube, nil, f.cfg.Kubernetes, application.WithPodExecutor(backend, []string{"demo"}), application.WithAuthorizer(policy))
	engine, err := router.NewWithAuth(f.cfg, service, nil, f.service)
	require.NoError(t, err)
	body := `{"namespace":"demo","pod":"redis-0","container":"redis","action":"redis_ping"}`
	request := func(token, payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster/pods/exec", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, 401, request("", body).Code)
	require.Zero(t, calls.Load())
	token := f.company(t, nil, nil)
	w := request(token, body)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "PONG")
	require.Contains(t, w.Body.String(), "uid-123")
	require.Equal(t, "wx-user", policy.user)
	for _, bad := range []string{strings.Replace(body, "redis_ping", "sh -c id", 1), strings.Replace(body, "demo", "default", 1), strings.Replace(body, "redis_ping", "unknown", 1), strings.TrimSuffix(body, "}") + `,"command":["sh","-c","id"]}`, strings.TrimSuffix(body, "}") + `,"timeoutSeconds":31}`} {
		expected := 400
		if strings.Contains(bad, `"namespace":"default"`) {
			expected = 403
		}
		require.Equal(t, expected, request(token, bad).Code)
	}
	require.Equal(t, int32(1), calls.Load())

	// Arbitrary text is forwarded as a single container-side argument, never run locally.
	shellBody := `{"namespace":"demo","pod":"redis-0","container":"redis","action":"shell","command":"printf hello | cat; id"}`
	require.Equal(t, 403, request(token, shellBody).Code) // explicit shell opt-in required
	shellPolicy := &shellTestPolicy{}
	application.WithShell(true, true, shellPolicy)(service)
	expectedCommand = []any{"/bin/sh", "-c", "printf hello | cat; id"}
	require.Equal(t, 200, request(token, shellBody).Code)
	require.Equal(t, "wx-user", shellPolicy.last.UserID)
	require.Equal(t, "uid-123", shellPolicy.last.PodUID)
	before := calls.Load()
	shellPolicy.deny = true
	blocked := request(token, shellBody)
	require.Equal(t, 403, blocked.Code)
	require.Contains(t, blocked.Body.String(), "SHELL_COMMAND_BLOCKED")
	require.Equal(t, before, calls.Load())
	shellPolicy.failure = true
	failed := request(token, shellBody)
	require.Equal(t, 403, failed.Code)
	require.Contains(t, failed.Body.String(), "SHELL_POLICY_ERROR")
	require.NotContains(t, failed.Body.String(), "private failure")
	require.Equal(t, before, calls.Load())
	// Disabling content interception still retains existing authentication and target checks.
	application.WithShell(true, false, shellPolicy)(service)
	require.Equal(t, 200, request(token, shellBody).Code)
	require.Equal(t, 401, request("", shellBody).Code)
	application.WithShell(false, true, interceptor.AllowAll{})(service)
	expectedCommand = []any{"redis-cli", "PING"}
	calls.Store(1)
	policy.deny = true
	require.Equal(t, 403, request(token, body).Code)
	require.Equal(t, int32(1), calls.Load())
	policy.deny = false
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pat, err := f.service.Create(authdomain.WithPrincipal(ctx, authdomain.Principal{UserID: "pat-user"}), authapp.CreateInput{Name: "exec-client", ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	server := httptest.NewServer(engine)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport{pat.Token}}}, nil)
	require.NoError(t, err)
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "exec_pod_command" {
			found = true
		}
		require.NotEqual(t, "pods_exec", tool.Name)
	}
	require.True(t, found)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "exec_pod_command", Arguments: map[string]any{"namespace": "demo", "pod": "redis-0", "container": "redis", "action": "redis_ping"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, "pat-user", policy.user)
	require.Equal(t, int32(2), calls.Load())
	require.NotNil(t, result.StructuredContent)
	application.WithShell(true, true, interceptor.AllowAll{})(service)
	expectedCommand = []any{"/bin/sh", "-c", "printf hello | cat; id"}
	shellArgs := map[string]any{"namespace": "demo", "pod": "redis-0", "container": "redis", "action": "shell", "command": "printf hello | cat; id"}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "exec_pod_command", Arguments: shellArgs})
	require.NoError(t, err)
	require.False(t, result.IsError)
	shellPolicy.failure = false
	shellPolicy.deny = true
	application.WithShell(true, true, shellPolicy)(service)
	before = calls.Load()
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "exec_pod_command", Arguments: shellArgs})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, fmt.Sprint(result.Content[0]), "SHELL_COMMAND_BLOCKED")
	require.Equal(t, before, calls.Load())

}

// A slow preflight must consume the same budget as execution, without submitting work.
func TestExecBudgetIncludesPodInspection(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer api.Close()
	provider := memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: test\n  context:\n    cluster: test\n    user: test\ncurrent-context: test\nusers:\n- name: test\n  user: {}\n", api.URL))
	cfg := config.KubernetesConfig{WriteEnabled: true, AllowedNamespaces: []string{"demo"}}
	kube, err := clusterk8s.New(cfg, provider)
	require.NoError(t, err)
	backend := &countingExecutor{}
	service := application.NewClusterService(kube, nil, cfg, application.WithPodExecutor(backend, []string{"demo"}))
	ctx := authdomain.WithPrincipal(context.Background(), authdomain.Principal{UserID: "user"})
	start := time.Now()
	out, err := service.ExecPodCommand(ctx, domain.PodExecInput{Namespace: "demo", Pod: "redis-0", Container: "redis", Action: "redis_ping", TimeoutSeconds: 1})
	require.NoError(t, err)
	require.True(t, out.TimedOut)
	require.Equal(t, "not_executed", out.Outcome)
	require.Zero(t, backend.calls.Load())
	require.GreaterOrEqual(t, out.DurationMS, int64(900))
	require.Less(t, time.Since(start), 3*time.Second)
}

type countingExecutor struct{ calls atomic.Int32 }

func (e *countingExecutor) Execute(context.Context, string, string, string, []string) (domain.PodExecOutput, error) {
	e.calls.Add(1)
	return domain.PodExecOutput{Outcome: "completed"}, nil
}

type shellTestPolicy struct {
	deny, failure bool
	last          interceptor.Request
}

func (p *shellTestPolicy) Check(_ context.Context, r interceptor.Request) (interceptor.Decision, error) {
	p.last = r
	if p.failure {
		return interceptor.Decision{}, fmt.Errorf("private failure")
	}
	return interceptor.Decision{Allowed: !p.deny}, nil
}
