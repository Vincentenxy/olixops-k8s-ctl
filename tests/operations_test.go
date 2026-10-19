package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	autoscalingv1 "k8s.io/api/autoscaling/v1"

	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/domain"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/router"
)

// A separate copy of this test executable emulates kubectl/Helm. Production
// code still uses exec.CommandContext, argv, stdin and real private temp files.
func TestMain(m *testing.M) {
	if os.Getenv("OLIXOPS_TEST_COMMAND_HELPER") == "1" {
		os.Exit(commandHelper())
	}
	os.Exit(m.Run())
}
func commandHelper() int {
	args := os.Args[1:]
	stdin, _ := io.ReadAll(os.Stdin)
	file, err := os.OpenFile(os.Getenv("OLIXOPS_TEST_COMMAND_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 2
	}
	record := map[string]any{"args": args, "stdin": string(stdin)}
	for i, arg := range args {
		if (arg == "--kubeconfig" || arg == "--values") && i+1 < len(args) {
			info, err := os.Stat(args[i+1])
			if err != nil || info.Mode().Perm() != 0o600 {
				return 3
			}
		}
	}
	_ = json.NewEncoder(file).Encode(record)
	_ = file.Close()
	command := ""
	index := 0
	for i, arg := range args {
		switch arg {
		case "apply", "list", "status", "history", "pull", "template", "upgrade", "rollback", "uninstall", "get", "rollout":
			command = arg
			index = i
		}
		if command != "" {
			break
		}
	}
	if command == os.Getenv("OLIXOPS_TEST_DELAY_COMMAND") {
		time.Sleep(5 * time.Second)
	}
	if command == os.Getenv("OLIXOPS_TEST_FAIL_COMMAND") {
		fmt.Fprint(os.Stderr, "sensitive-stderr-token")
		return 4
	}
	flag := func(name string) string {
		for i, arg := range args {
			if arg == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch command {
	case "apply":
		fmt.Print("ConfigMap/sensitive-value configured")
	case "rollout":
		fmt.Print("rollout requested")
	case "list":
		fmt.Print(`[{"name":"web","namespace":"demo","revision":"2","status":"deployed","chart":"nginx-1.0","app_version":"1.0"}]`)
	case "status":
		fmt.Print(`{"name":"web","namespace":"demo","version":2,"info":{"status":"deployed","notes":"sensitive-notes"},"config":{"password":"sensitive-values"},"manifest":"sensitive-manifest","hooks":["sensitive-hook"],"chart":{"metadata":{"name":"nginx","version":"1.0","appVersion":"1.0"}}}`)
	case "history":
		fmt.Print(`[{"revision":1,"status":"superseded","chart":"nginx-0.9","app_version":"0.9","description":"sensitive-description"},{"revision":2,"status":"deployed","chart":"nginx-1.0","app_version":"1.0"}]`)
	case "get":
		if args[index+1] != "manifest" {
			return 6
		}
		manifest := os.Getenv("OLIXOPS_TEST_STORED_MANIFEST")
		if manifest == "" {
			manifest = demoManifest
		}
		fmt.Print(manifest)
	case "pull":
		if err := os.WriteFile(filepath.Join(flag("--destination"), "nginx-1.0.tgz"), []byte("fake chart"), 0o600); err != nil {
			return 7
		}
	case "template":
		manifest := os.Getenv("OLIXOPS_TEST_RENDER_MANIFEST")
		if manifest == "" {
			manifest = demoManifest
		}
		fmt.Print(manifest)
	case "upgrade", "rollback", "uninstall":
		fmt.Print("sensitive-notes")
	default:
		return 5
	}
	return 0
}

const demoManifest = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: web\n  namespace: demo\ndata:\n  sample: value\n"

type requestRecord struct {
	Method, Path, Query string
	Body                []byte
}
type fakeCluster struct {
	mu       sync.Mutex
	requests []requestRecord
}

func (f *fakeCluster) snapshot() []requestRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]requestRecord(nil), f.requests...)
}
func (f *fakeCluster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, requestRecord{r.Method, r.URL.Path, r.URL.RawQuery, body})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case path == "/apis/apps/v1":
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"APIResourceList","groupVersion":"apps/v1","resources":[{"name":"deployments","namespaced":true,"kind":"Deployment"},{"name":"statefulsets","namespaced":true,"kind":"StatefulSet"},{"name":"daemonsets","namespaced":true,"kind":"DaemonSet"}]}`)
	case path == "/api/v1":
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"pods","namespaced":true,"kind":"Pod"},{"name":"configmaps","namespaced":true,"kind":"ConfigMap"}]}`)
	case strings.HasSuffix(path, "/scale"):
		fmt.Fprint(w, `{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"web","resourceVersion":"7"},"spec":{"replicas":0},"status":{"replicas":1}}`)
	case strings.HasSuffix(path, "/log"):
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "nginx started\nGET / 200\n")
	case strings.HasSuffix(path, "/events"):
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"EventList","metadata":{"continue":"next-page"},"items":[{"metadata":{"name":"web.event"},"type":"Normal","reason":"Started","message":"Started container nginx","involvedObject":{"kind":"Pod","name":"web-0","namespace":"demo"},"count":1}]}`)
	case r.Method == http.MethodDelete:
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Success"}`)
	case path == "/api/v1/namespaces" && r.Method == http.MethodPost:
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"demo"}}`)
	case path == "/api/v1/namespaces/demo":
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"demo","uid":"namespace-uid"}}`)
	case strings.HasSuffix(path, "/configmaps/web"):
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"web","namespace":"demo","uid":"cm-uid"},"data":{"password":"sensitive-config-data"},"binaryData":{"binary":"c2VjcmV0"}}`)
	case strings.HasSuffix(path, "/pods/web-0"):
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"web-0","namespace":"demo","uid":"pod-uid"},"spec":{"containers":[{"name":"nginx","image":"nginx:stable","env":[{"name":"PASSWORD","value":"sensitive-env"}],"command":["sensitive-command"],"args":["sensitive-args"]}]}}`)
	case strings.HasSuffix(path, "/replicasets"):
		fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"ReplicaSetList","items":[{"metadata":{"name":"web-r1","annotations":{"deployment.kubernetes.io/revision":"1"},"ownerReferences":[{"uid":"dep-uid","kind":"Deployment","name":"web","controller":true}]}},{"metadata":{"name":"someone-else","annotations":{"deployment.kubernetes.io/revision":"99"},"ownerReferences":[{"uid":"other-uid","controller":true}]}}]}`)
	case strings.HasSuffix(path, "/deployments"):
		fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"DeploymentList","metadata":{"continue":"next-workloads"},"items":[{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"demo","uid":"dep-uid"},"spec":{"replicas":1},"status":{"readyReplicas":1}}]}`)
	case strings.HasSuffix(path, "/deployments/web") || strings.HasSuffix(path, "/statefulsets/web") || strings.HasSuffix(path, "/daemonsets/web"):
		fmt.Fprint(w, `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"demo","uid":"dep-uid"},"spec":{"replicas":1}}`)
	default:
		w.WriteHeader(404)
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","reason":"NotFound","code":404}`)
	}
}

type recordingAuthorizer struct {
	mu       sync.Mutex
	requests []domain.AuthorizationRequest
	deny     bool
}

func (a *recordingAuthorizer) Authorize(_ context.Context, r domain.AuthorizationRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r)
	if a.deny {
		return fmt.Errorf("test authorization denied")
	}
	return nil
}
func (a *recordingAuthorizer) snapshot() []domain.AuthorizationRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]domain.AuthorizationRequest(nil), a.requests...)
}

type operationFixture struct {
	service    *application.ClusterService
	cfg        *config.Config
	api        *fakeCluster
	commandLog string
	authorizer *recordingAuthorizer
}

func newOperationFixture(t *testing.T) operationFixture {
	t.Helper()
	fake := &fakeCluster{}
	api := httptest.NewServer(fake)
	t.Cleanup(api.Close)
	provider := memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: selected\n  context:\n    cluster: test\n    user: test\ncurrent-context: selected\nusers:\n- name: test\n  user: {}\n", api.URL))
	cfg := &config.Config{Server: config.ServerConfig{Mode: "test"}, App: config.AppConfig{Version: "test"}, MCP: config.MCPConfig{Enabled: true, WriteEnabled: true}, Kubernetes: config.KubernetesConfig{WriteEnabled: true, AllowedNamespaces: []string{"demo"}, CommandTimeoutSecond: 10}}
	client, err := clusterk8s.New(cfg.Kubernetes, provider)
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	logPath := filepath.Join(t.TempDir(), "commands.jsonl")
	t.Setenv("OLIXOPS_TEST_COMMAND_HELPER", "1")
	t.Setenv("OLIXOPS_TEST_COMMAND_LOG", logPath)
	runner := execctl.New(execctl.Config{Provider: provider, Reference: kubeconfig.Reference{Context: "selected"}, Kubectl: executable, Helm: executable, TimeoutSeconds: 10})
	authorizer := &recordingAuthorizer{}
	service := application.NewClusterService(client, runner, cfg.Kubernetes, application.WithAuthorizer(authorizer))
	return operationFixture{service, cfg, fake, logPath, authorizer}
}

type commandRecord struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
}

func readCommands(t *testing.T, path string) []commandRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	result := []commandRecord{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record commandRecord
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		result = append(result, record)
	}
	return result
}
func (f operationFixture) session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	engine, err := router.New(f.cfg, f.service)
	require.NoError(t, err)
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "ops-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/api/v1/pub/mcp"}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func callOperation(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, fail bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.Equal(t, fail, result.IsError, "%s: %+v", name, result)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	return string(data)
}

func TestMCPOperationsEndToEnd(t *testing.T) {
	f := newOperationFixture(t)
	session := f.session(t)
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 28)
	writes := map[string]bool{"dry_run_manifest": true, "apply_manifest": true, "helm_upgrade": true, "scale_workload": true, "restart_workload": true, "rollback_workload": true, "delete_resource": true, "helm_rollback": true, "helm_uninstall": true, "create_namespace": true, "delete_namespace": true, "update_namespace_metadata": true, "update_configmap": true}
	for _, tool := range tools.Tools {
		require.Equal(t, !writes[tool.Name], tool.Annotations.ReadOnlyHint, tool.Name)
		if writes[tool.Name] && tool.Name != "dry_run_manifest" && tool.Name != "create_namespace" {
			require.True(t, *tool.Annotations.DestructiveHint, tool.Name)
		}
	}
	call := func(name string, args map[string]any) string { return callOperation(t, session, name, args, false) }
	detail := call("get_resource", map[string]any{"namespace": "demo", "kind": "Pod", "name": "web-0"})
	require.Contains(t, detail, "nginx:stable")
	require.NotContains(t, detail, "sensitive-env")
	require.NotContains(t, detail, "sensitive-command")
	require.NotContains(t, detail, "sensitive-args")
	detail = call("get_resource", map[string]any{"namespace": "demo", "kind": "ConfigMap", "name": "web"})
	require.Contains(t, detail, "dataKeys")
	require.NotContains(t, detail, "sensitive-config-data")
	require.NotContains(t, detail, "c2VjcmV0")
	page := call("list_resource_objects", map[string]any{"namespace": "demo", "kind": "Deployment", "labelSelector": "app=web", "limit": 2, "continue": "first-page"})
	require.Contains(t, page, "next-workloads")
	events := call("list_events", map[string]any{"namespace": "demo", "kind": "Pod", "name": "web-0", "limit": 2})
	require.Contains(t, events, "Started container nginx")
	require.Contains(t, events, "next-page")
	logs := call("get_pod_logs", map[string]any{"namespace": "demo", "pod": "web-0", "container": "nginx", "tailLines": 50, "sinceSeconds": 60, "previous": true, "timestamps": true})
	require.Contains(t, logs, "GET / 200")
	call("list_helm_releases", map[string]any{"namespace": "demo", "limit": 2, "offset": 1})
	status := call("get_helm_status", map[string]any{"namespace": "demo", "release": "web"})
	require.Contains(t, status, `"status":"deployed"`)
	for _, secret := range []string{"sensitive-notes", "sensitive-values", "sensitive-manifest", "sensitive-hook"} {
		require.NotContains(t, status, secret)
	}
	require.NotContains(t, call("get_helm_history", map[string]any{"namespace": "demo", "release": "web"}), "sensitive-description")
	call("dry_run_manifest", map[string]any{"namespace": "demo", "manifest": demoManifest, "serverSide": true})
	apply := call("apply_manifest", map[string]any{"namespace": "demo", "manifest": demoManifest, "serverSide": true})
	require.NotContains(t, apply, "sensitive-value")
	call("scale_workload", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "web", "replicas": 0})
	call("scale_workload", map[string]any{"namespace": "demo", "kind": "StatefulSet", "name": "web", "replicas": 3})
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet"} {
		call("restart_workload", map[string]any{"namespace": "demo", "kind": kind, "name": "web"})
	}
	history := call("get_workload_history", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "web"})
	require.Contains(t, history, `"revision":1`)
	require.NotContains(t, history, "someone-else")
	call("rollback_workload", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "web", "revision": 1})
	call("delete_resource", map[string]any{"namespace": "demo", "kind": "Pod", "name": "web-0"})
	call("update_configmap", map[string]any{"namespace": "demo", "name": "web", "data": map[string]string{"mode": "demo"}})
	call("update_namespace_metadata", map[string]any{"namespace": "demo", "labels": map[string]string{"environment": "test"}, "annotations": map[string]string{"owner": "platform"}, "removeLabels": []string{"old-label"}})
	call("create_namespace", map[string]any{"namespace": "demo"})
	call("delete_namespace", map[string]any{"namespace": "demo"})
	for _, name := range []string{"helm_upgrade", "helm_rollback", "helm_uninstall"} {
		args := map[string]any{"namespace": "demo", "release": "web", "wait": true}
		if name == "helm_upgrade" {
			args["chart"] = "demo/nginx"
			args["version"] = "1.0.0"
			args["valuesYaml"] = "replicaCount: 1"
		}
		if name == "helm_rollback" {
			args["revision"] = 1
		}
		require.NotContains(t, call(name, args), "sensitive-notes")
	}

	requests := f.api.snapshot()
	var scaledZero, scaledThree, deletedPod, deletedNS, restarted, updated, updatedNamespace bool
	for _, r := range requests {
		if strings.HasSuffix(r.Path, "/scale") && r.Method == "PUT" {
			var body map[string]any
			require.NoError(t, json.Unmarshal(r.Body, &body))
			var scale autoscalingv1.Scale
			require.NoError(t, json.Unmarshal(r.Body, &scale))
			if strings.Contains(r.Path, "deployments") {
				require.Equal(t, int32(0), scale.Spec.Replicas)
				scaledZero = true
			} else {
				require.Equal(t, int32(3), scale.Spec.Replicas)
				scaledThree = true
			}
			require.Equal(t, "7", body["metadata"].(map[string]any)["resourceVersion"])
		}
		if strings.HasSuffix(r.Path, "/pods/web-0") && r.Method == "DELETE" {
			require.Contains(t, string(r.Body), `"uid":"pod-uid"`)
			require.Contains(t, string(r.Body), `"propagationPolicy":"Background"`)
			deletedPod = true
		}
		if r.Path == "/api/v1/namespaces/demo" && r.Method == "DELETE" {
			require.Contains(t, string(r.Body), "namespace-uid")
			deletedNS = true
		}
		if strings.Contains(r.Path, "/deployments/web") && r.Method == "PATCH" {
			require.Contains(t, string(r.Body), "kubectl.kubernetes.io/restartedAt")
			restarted = true
		}
		if strings.HasSuffix(r.Path, "/configmaps/web") && r.Method == "PATCH" {
			require.JSONEq(t, `{"data":{"mode":"demo"}}`, string(r.Body))
			updated = true
		}
		if r.Path == "/api/v1/namespaces/demo" && r.Method == "PATCH" {
			require.JSONEq(t, `{"metadata":{"annotations":{"owner":"platform"},"labels":{"environment":"test","old-label":null}}}`, string(r.Body))
			updatedNamespace = true
		}
		if strings.HasSuffix(r.Path, "/log") {
			require.Contains(t, r.Query, "tailLines=50")
			require.Contains(t, r.Query, "sinceSeconds=60")
			require.Contains(t, r.Query, "previous=true")
			require.NotContains(t, r.Query, "follow=true")
		}
		if strings.HasSuffix(r.Path, "/deployments") {
			require.Contains(t, r.Query, "limit=2")
			require.Contains(t, r.Query, "labelSelector=app%3Dweb")
		}
	}
	require.True(t, scaledZero && scaledThree && deletedPod && deletedNS && restarted && updated && updatedNamespace)
	commands := readCommands(t, f.commandLog)
	var applyCount, dryCount int
	var downloaded, rendered, upgraded bool
	for _, command := range commands {
		args := strings.Join(command.Args, " ")
		// Temp files must be cleaned up after each operation.
		for i, arg := range command.Args {
			if arg == "--kubeconfig" || arg == "--values" {
				_, err := os.Stat(command.Args[i+1])
				require.True(t, os.IsNotExist(err), command.Args[i+1])
			}
		}
		if strings.Contains(args, " apply ") {
			require.Contains(t, args, "--context selected")
			require.NotContains(t, args, "--kube-context")
			require.Equal(t, demoManifest, command.Stdin)
			applyCount++
			if strings.Contains(args, "--dry-run=server") {
				dryCount++
			}
		} else if strings.Contains(args, " rollout ") {
			require.Contains(t, args, "--context selected")
			require.Contains(t, args, "--to-revision 1")
		} else {
			require.Contains(t, args, "--kube-context selected")
		}
		if strings.Contains(args, " pull ") {
			require.Contains(t, args, "--version 1.0.0")
			downloaded = true
		}
		if strings.Contains(args, " template ") {
			require.Contains(t, args, ".tgz")
			require.Contains(t, args, "--include-crds")
			require.Contains(t, args, "--no-hooks")
			rendered = true
		}
		if strings.Contains(args, " upgrade ") {
			require.Contains(t, args, ".tgz")
			require.Contains(t, args, "--reset-values")
			require.Contains(t, args, "--wait")
			require.Contains(t, args, "--no-hooks")
			require.NotContains(t, args, "--create-namespace")
			upgraded = true
		}
	}
	require.Equal(t, 2, applyCount)
	require.Equal(t, 1, dryCount)
	require.True(t, downloaded && rendered && upgraded)
	actions := map[string]bool{}
	for _, request := range f.authorizer.snapshot() {
		actions[request.Action] = true
		if request.Write {
			require.NotEmpty(t, request.Targets)
		}
	}
	for _, action := range []string{"workload.history", "resource.get", "resource.list", "event.list", "pod.logs", "helm.list", "helm.status", "helm.history", "manifest.preview", "manifest.apply", "helm.upgrade", "workload.scale", "workload.restart", "workload.rollback", "resource.delete", "helm.rollback", "helm.uninstall", "namespace.create", "namespace.delete", "namespace.update", "configmap.update"} {
		require.True(t, actions[action], action)
	}
}

func TestNamespaceMetadataREST(t *testing.T) {
	f := newOperationFixture(t)
	f.cfg.Control.APIKey = "rest-key"
	engine, err := router.New(f.cfg, f.service)
	require.NoError(t, err)
	server := httptest.NewServer(engine)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/cluster/namespaces/demo/metadata", strings.NewReader(`{"labels":{"team":"platform"},"annotations":{"owner":"ops"}}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Olixops-Control-Key", "rest-key")
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	invalid, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/cluster/namespaces/demo/metadata", strings.NewReader(`{"labels":{"team":"platform"},"spec":{"finalizers":[]}}`))
	require.NoError(t, err)
	invalid.Header.Set("Content-Type", "application/json")
	invalid.Header.Set("X-Olixops-Control-Key", "rest-key")
	invalidResponse, err := http.DefaultClient.Do(invalid)
	require.NoError(t, err)
	invalidResponse.Body.Close()
	require.Equal(t, http.StatusBadRequest, invalidResponse.StatusCode)

	requests := f.api.snapshot()
	var patched bool
	for _, request := range requests {
		if request.Path == "/api/v1/namespaces/demo" && request.Method == http.MethodPatch {
			require.JSONEq(t, `{"metadata":{"annotations":{"owner":"ops"},"labels":{"team":"platform"}}}`, string(request.Body))
			patched = true
		}
	}
	require.True(t, patched)
}

func TestOperationGuardsAndReservedAuthorization(t *testing.T) {
	f := newOperationFixture(t)
	session := f.session(t)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"apply_manifest", map[string]any{"namespace": "private", "manifest": demoManifest}},
		{"dry_run_manifest", map[string]any{"namespace": "demo", "manifest": strings.ReplaceAll(demoManifest, "ConfigMap", "Secret")}},
		{"scale_workload", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "web", "replicas": -1}},
		{"scale_workload", map[string]any{"namespace": "demo", "kind": "DaemonSet", "name": "web", "replicas": 1}},
		{"scale_workload", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "web"}},
		{"restart_workload", map[string]any{"namespace": "demo", "kind": "Secret", "name": "web"}},
		{"rollback_workload", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "--all", "revision": 1}},
		{"delete_resource", map[string]any{"namespace": "demo", "kind": "ClusterRole", "name": "admin"}},
		{"delete_resource", map[string]any{"namespace": "demo", "kind": "Pod", "name": "../../private"}},
		{"delete_namespace", map[string]any{"namespace": "default"}},
		{"create_namespace", map[string]any{"namespace": "private"}},
		{"update_namespace_metadata", map[string]any{"namespace": "demo", "labels": map[string]string{"../invalid": "x"}}},
		{"update_namespace_metadata", map[string]any{"namespace": "demo", "labels": map[string]string{"environment": "test"}, "removeLabels": []string{"environment"}}},
		{"update_configmap", map[string]any{"namespace": "demo", "name": "web", "data": map[string]string{"../invalid": "x"}}},
		{"get_pod_logs", map[string]any{"namespace": "demo", "pod": "web-0", "tailLines": -1}},
		{"get_pod_logs", map[string]any{"namespace": "demo", "pod": "web-0", "container": "--follow"}},
		{"get_resource", map[string]any{"namespace": "demo", "kind": "Secret", "name": "x"}},
		{"list_events", map[string]any{"namespace": "private"}},
		{"helm_upgrade", map[string]any{"namespace": "demo", "release": "web", "chart": "/etc/passwd"}},
		{"helm_upgrade", map[string]any{"namespace": "demo", "release": "web", "chart": "--post-renderer"}},
		{"helm_upgrade", map[string]any{"namespace": "demo", "release": "web", "chart": "demo/nginx", "version": "--debug"}},
		{"helm_rollback", map[string]any{"namespace": "demo", "release": "web", "revision": 0}},
		{"helm_uninstall", map[string]any{"namespace": "demo", "release": "--all"}},
	}
	for _, test := range cases {
		callOperation(t, session, test.name, test.args, true)
	}
	require.Empty(t, f.api.snapshot())
	require.Empty(t, readCommands(t, f.commandLog))

	// Test adapter proves the seam is actually invoked before any Kubernetes/CLI effects.
	// This is deliberately not a production permissions implementation.
	f.authorizer.mu.Lock()
	f.authorizer.deny = true
	f.authorizer.mu.Unlock()
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{"apply_manifest", map[string]any{"namespace": "demo", "manifest": demoManifest}},
		{"dry_run_manifest", map[string]any{"namespace": "demo", "manifest": demoManifest}},
		{"scale_workload", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "web", "replicas": 1}},
		{"restart_workload", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "web"}},
		{"delete_resource", map[string]any{"namespace": "demo", "kind": "Pod", "name": "web-0"}},
		{"rollback_workload", map[string]any{"namespace": "demo", "kind": "Deployment", "name": "web", "revision": 1}},
		{"helm_upgrade", map[string]any{"namespace": "demo", "release": "web", "chart": "demo/nginx"}},
		{"helm_rollback", map[string]any{"namespace": "demo", "release": "web", "revision": 1}},
		{"helm_uninstall", map[string]any{"namespace": "demo", "release": "web"}},
		{"create_namespace", map[string]any{"namespace": "demo"}},
		{"delete_namespace", map[string]any{"namespace": "demo"}},
		{"update_namespace_metadata", map[string]any{"namespace": "demo", "labels": map[string]string{"environment": "test"}}},
		{"update_configmap", map[string]any{"namespace": "demo", "name": "web", "data": map[string]string{"key": "value"}}},
		{"get_pod_logs", map[string]any{"namespace": "demo", "pod": "web-0"}},
	} {
		callOperation(t, session, test.name, test.args, true)
	}
	require.Empty(t, f.api.snapshot())
	require.Empty(t, readCommands(t, f.commandLog))
	_, _, err := f.service.ApplyManifest(context.Background(), demoManifest, "demo", false, false)
	require.Error(t, err, "REST uses the same application method and reserved authorizer")
	_, err = f.service.HelmUpgrade(context.Background(), "web", "demo/nginx", "demo", "", false)
	require.Error(t, err)
	require.Empty(t, readCommands(t, f.commandLog))
}

func TestMCPGlobalWriteSwitch(t *testing.T) {
	f := newOperationFixture(t)
	f.cfg.Kubernetes.WriteEnabled = false
	disabled := application.NewClusterService(nil, nil, f.cfg.Kubernetes)
	_, _, err := disabled.ApplyManifest(context.Background(), demoManifest, "demo", false, false)
	require.ErrorContains(t, err, "disabled")
	_, err = disabled.ScaleWorkload(context.Background(), "demo", "Deployment", "web", 0)
	require.ErrorContains(t, err, "disabled")
	_, err = disabled.RestartWorkload(context.Background(), "demo", "Deployment", "web")
	require.ErrorContains(t, err, "disabled")
	_, err = disabled.DeleteResource(context.Background(), "demo", "Pod", "web-0")
	require.ErrorContains(t, err, "disabled")
	_, err = disabled.DeleteNamespace(context.Background(), "demo")
	require.ErrorContains(t, err, "disabled")
	_, err = disabled.UpdateNamespaceMetadata(context.Background(), "demo", application.NamespaceMetadataChanges{Labels: map[string]string{"environment": "test"}})
	require.ErrorContains(t, err, "disabled")
	require.Empty(t, f.api.snapshot())
	require.Empty(t, readCommands(t, f.commandLog))
}

func TestHelmManifestRejectionAndCommandFailure(t *testing.T) {
	f := newOperationFixture(t)
	session := f.session(t)
	t.Setenv("OLIXOPS_TEST_RENDER_MANIFEST", strings.ReplaceAll(demoManifest, "namespace: demo", "namespace: private"))
	callOperation(t, session, "helm_upgrade", map[string]any{"namespace": "demo", "release": "web", "chart": "demo/nginx"}, true)
	for _, command := range readCommands(t, f.commandLog) {
		require.NotContains(t, command.Args, "upgrade")
	}
	t.Setenv("OLIXOPS_TEST_STORED_MANIFEST", strings.ReplaceAll(demoManifest, "ConfigMap", "Secret"))
	callOperation(t, session, "helm_uninstall", map[string]any{"namespace": "demo", "release": "web"}, true)
	callOperation(t, session, "helm_rollback", map[string]any{"namespace": "demo", "release": "web", "revision": 1}, true)
	for _, command := range readCommands(t, f.commandLog) {
		require.NotContains(t, command.Args, "uninstall")
		require.NotContains(t, command.Args, "rollback")
	}
	t.Setenv("OLIXOPS_TEST_FAIL_COMMAND", "apply")
	response := callOperation(t, session, "apply_manifest", map[string]any{"namespace": "demo", "manifest": demoManifest}, true)
	require.NotContains(t, response, "sensitive-stderr-token")
}

func TestPodLogCaptureBound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/namespaces/demo/pods/web/log", r.URL.Path)
		require.Equal(t, "200", r.URL.Query().Get("tailLines"))
		require.Equal(t, "262145", r.URL.Query().Get("limitBytes"))
		_, _ = io.WriteString(w, strings.Repeat("x", (256<<10)+1024))
	}))
	defer upstream.Close()
	provider := memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: t\n  cluster:\n    server: %s\ncontexts:\n- name: t\n  context:\n    cluster: t\ncurrent-context: t\n", upstream.URL))
	cfg := config.KubernetesConfig{AllowedNamespaces: []string{"demo"}}
	client, err := clusterk8s.New(cfg, provider)
	require.NoError(t, err)
	service := application.NewClusterService(client, nil, cfg)
	logs, err := service.GetPodLogs(context.Background(), clusterk8s.LogOptions{Namespace: "demo", Pod: "web"})
	require.NoError(t, err)
	require.True(t, logs.Truncated)
	require.Len(t, logs.Text, 256<<10)
}

func TestProtectedNamespacesAndManifestGVK(t *testing.T) {
	service := application.NewClusterService(nil, nil, config.KubernetesConfig{WriteEnabled: true})
	for _, namespace := range []string{"default", "kube-system", "kube-public", "kube-node-lease"} {
		_, err := service.DeleteNamespace(context.Background(), namespace)
		require.ErrorContains(t, err, "protected")
	}
	for _, manifest := range []string{
		strings.ReplaceAll(demoManifest, "apiVersion: v1", "apiVersion: evil.example/v1"),
		strings.ReplaceAll(demoManifest, "name: web", "name: ../../other"),
		strings.ReplaceAll(demoManifest, "kind: ConfigMap", "kind: ClusterRole"),
	} {
		_, err := application.ValidateManifest(manifest, "demo", nil)
		require.Error(t, err)
	}
	for _, kind := range []string{"Job", "CronJob"} {
		_, err := application.ValidateManifest("apiVersion: batch/v1\nkind: "+kind+"\nmetadata:\n  name: demo\n", "demo", nil)
		require.NoError(t, err)
	}
}

func TestExternalCommandCancellation(t *testing.T) {
	f := newOperationFixture(t)
	t.Setenv("OLIXOPS_TEST_DELAY_COMMAND", "status")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := f.service.GetHelmStatus(ctx, "demo", "web")
	require.Error(t, err)
	require.Less(t, time.Since(start), 2*time.Second, "canceling a tool must terminate its CLI process")
}
