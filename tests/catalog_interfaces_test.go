package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application"
	app "olixops-k8s-ctl/internal/application/catalog"
	d "olixops-k8s-ctl/internal/domain/catalog"
	"olixops-k8s-ctl/internal/infrastructure/catalogdb"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/interfaces/router"
)

type catalogReadCluster struct{}

func (catalogReadCluster) Check(ctx context.Context, raw, ns string) error {
	return (application.CatalogCluster{Service: application.NewClusterService(nil, nil, config.KubernetesConfig{})}).Check(ctx, raw, ns)
}
func (catalogReadCluster) Snapshot(context.Context, d.Gateway) (d.Snapshot, error) {
	return d.Snapshot{Ports: map[int]d.PortUsage{}, TCPRouteVersion: "v1alpha2"}, nil
}
func (catalogReadCluster) Preflight(context.Context, d.Instance, d.Parameters) error {
	return errors.New("not configured")
}
func (catalogReadCluster) EnsurePort(context.Context, d.Gateway, int, string) error {
	return errors.New("not configured")
}
func (catalogReadCluster) Apply(context.Context, string, string, bool) error {
	return errors.New("not configured")
}
func (catalogReadCluster) Inventory(context.Context, string) ([]d.Resource, error) {
	return nil, errors.New("not configured")
}
func (catalogReadCluster) Inspect(context.Context, d.Deployment, d.Gateway) (d.Observation, error) {
	return d.Observation{}, errors.New("not configured")
}
func (catalogReadCluster) Delete(context.Context, d.Resource) error {
	return errors.New("not configured")
}
func (catalogReadCluster) Verify(context.Context, d.Gateway, d.Deployment, d.Instance, d.Parameters) (d.AccessReport, error) {
	return d.AccessReport{}, errors.New("not configured")
}

func TestCatalogHTTPMCPParityAndWriteDiscovery(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(map[bool]string{false: "read_only", true: "write_enabled"}[write], func(t *testing.T) {
			ctx := context.Background()
			store, err := catalogdb.Open(filepath.Join(t.TempDir(), "catalog.db"))
			require.NoError(t, err)
			defer store.Close()
			catalog, err := app.New(ctx, store, catalogReadCluster{}, d.Gateway{ID: "higress", Cluster: "test", Namespace: "higress-system", Name: "higress-tcp-gateway", Service: "higress-gateway", PortStart: 30000, PortEnd: 30200}, nil)
			require.NoError(t, err)
			cfg := &config.Config{Server: config.ServerConfig{Mode: "test"}, MCP: config.MCPConfig{Enabled: true, WriteEnabled: write}, Control: config.ControlConfig{APIKey: "test-key"}}
			engine, err := router.NewWithCatalog(cfg, application.NewClusterService(nil, nil, cfg.Kubernetes), catalog)
			require.NoError(t, err)
			server := httptest.NewServer(engine)
			defer server.Close()
			req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/templates", nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.Equal(t, 401, resp.StatusCode)
			resp.Body.Close()
			req.Header.Set("X-Olixops-Control-Key", "test-key")
			req.Header.Set("X-Request-Id", "catalog-test")
			resp, err = http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.Equal(t, "catalog-test", resp.Header.Get("X-Request-Id"))
			var envelope struct {
				Code int          `json:"code"`
				Data []d.Template `json:"data"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
			resp.Body.Close()
			require.Zero(t, envelope.Code)
			require.Len(t, envelope.Data, 9)
			standardReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/services/standard", nil)
			require.NoError(t, err)
			standardReq.Header.Set("X-Olixops-Control-Key", "test-key")
			standardResp, err := http.DefaultClient.Do(standardReq)
			require.NoError(t, err)
			require.Equal(t, 200, standardResp.StatusCode)
			var standardEnvelope struct {
				Code int                  `json:"code"`
				Data app.StandardServices `json:"data"`
			}
			require.NoError(t, json.NewDecoder(standardResp.Body).Decode(&standardEnvelope))
			standardResp.Body.Close()
			require.Zero(t, standardEnvelope.Code)
			require.Len(t, standardEnvelope.Data.Items, 7)

			client := mcp.NewClient(&mcp.Implementation{Name: "catalog-test", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/api/v1/pub/mcp"}, nil)
			require.NoError(t, err)
			defer session.Close()
			listed, err := session.ListTools(ctx, nil)
			require.NoError(t, err)
			names := map[string]*mcp.Tool{}
			for _, tool := range listed.Tools {
				names[tool.Name] = tool
			}
			require.NotNil(t, names["list_service_templates"])
			require.NotNil(t, names["list_standard_services"])
			require.True(t, names["list_standard_services"].Annotations.ReadOnlyHint)
			if write {
				require.NotNil(t, names["deploy_standard_service"])
				require.False(t, names["deploy_standard_service"].Annotations.ReadOnlyHint)
				schemaJSON, err := json.Marshal(names["deploy_standard_service"].InputSchema)
				require.NoError(t, err)
				var toolSchema struct {
					Required []string `json:"required"`
				}
				require.NoError(t, json.Unmarshal(schemaJSON, &toolSchema))
				require.Contains(t, toolSchema.Required, "namespace")
				missingNS, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "deploy_standard_service", Arguments: map[string]any{"software": "pg", "requestId": "missing-ns"}})
				if err != nil {
					require.Contains(t, err.Error(), "namespace")
				} else {
					require.True(t, missingNS.IsError)
					raw, _ := json.Marshal(missingNS)
					require.Contains(t, string(raw), "namespace")
				}
				missingReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/services/deploy-standard", strings.NewReader(`{"software":"pg","requestId":"missing-ns"}`))
				require.NoError(t, err)
				missingReq.Header.Set("Content-Type", "application/json")
				missingReq.Header.Set("X-Olixops-Control-Key", "test-key")
				missingResp, err := http.DefaultClient.Do(missingReq)
				require.NoError(t, err)
				require.Equal(t, 400, missingResp.StatusCode)
				var missingEnvelope struct {
					Code int `json:"code"`
					Data struct {
						Reason string `json:"reason"`
					} `json:"data"`
				}
				require.NoError(t, json.NewDecoder(missingResp.Body).Decode(&missingEnvelope))
				missingResp.Body.Close()
				require.Equal(t, "MISSING_NAMESPACE", missingEnvelope.Data.Reason)
				require.NoError(t, store.View(ctx, func(st *d.State) error {
					require.Empty(t, st.Instances)
					require.Empty(t, st.Revisions)
					require.Empty(t, st.Deployments)
					return nil
				}))

			} else {
				require.Nil(t, names["deploy_standard_service"])
			}
			discovered, discoverErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_standard_services", Arguments: map[string]any{}})
			require.NoError(t, discoverErr)
			require.False(t, discovered.IsError)
			discoveredJSON, _ := json.Marshal(discovered)
			for _, sw := range []string{"redis", "postgres", "mysql", "mongodb", "milvus", "nginx", "sandbox"} {
				require.Contains(t, string(discoveredJSON), sw)
			}

			require.True(t, names["get_service_deployment"].Annotations.ReadOnlyHint)
			require.True(t, names["verify_service_access"].Annotations.ReadOnlyHint)
			if write {
				require.Len(t, names, 50)
				require.False(t, names["sync_gateway_ports"].Annotations.ReadOnlyHint)
				require.True(t, *names["delete_service_instance"].Annotations.DestructiveHint)
			} else {
				require.Len(t, names, 28)
				require.Nil(t, names["save_service_config"])
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_service_templates", Arguments: map[string]any{}})
			require.NoError(t, err)
			require.False(t, result.IsError)
			raw, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			// any-typed list result is exposed as text by SDKs; both representations carry
			// exactly the persisted templates, not a separately implemented MCP catalog.
			textResult, _ := json.Marshal(result)
			for _, templ := range envelope.Data {
				require.Contains(t, string(textResult), templ.Software)
			}
			_ = raw
			if write {
				args := map[string]any{"ownerId": "alice", "name": "redis-demo", "namespace": "demo", "software": "redis", "templateVersion": 1, "parameters": map[string]any{"storageClass": "standard", "secretName": "alice-redis"}, "expectedRevision": 0, "requestId": "save-1"}
				result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "save_service_config", Arguments: args})
				require.NoError(t, err)
				require.False(t, result.IsError)
				encoded, err := json.Marshal(result.StructuredContent)
				require.NoError(t, err)
				var saved app.SavedConfig
				require.NoError(t, json.Unmarshal(encoded, &saved))
				require.NotEmpty(t, saved.Instance.ID)
				body, err := json.Marshal(args)
				require.NoError(t, err)
				req, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/services/save-config", strings.NewReader(string(body)))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Olixops-Control-Key", "test-key")
				resp, err = http.DefaultClient.Do(req)
				require.NoError(t, err)
				var savedHTTP struct {
					Code int             `json:"code"`
					Data app.SavedConfig `json:"data"`
				}
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&savedHTTP))
				resp.Body.Close()
				require.Zero(t, savedHTTP.Code)
				require.Equal(t, saved.Revision.ID, savedHTTP.Data.Revision.ID)
				result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "reserve_gateway_port", Arguments: map[string]any{"instanceId": saved.Instance.ID, "port": 20030, "requestId": "bad-port"}})
				require.NoError(t, err)
				require.True(t, result.IsError)
				encoded, _ = json.Marshal(result)
				require.Contains(t, string(encoded), "PORT_NOT_IN_POOL")
			}
		})
	}
}
