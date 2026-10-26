package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/domain"
	d "olixops-k8s-ctl/internal/domain/catalog"
	"olixops-k8s-ctl/internal/infrastructure/config"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

type namespaceAuthorizer struct {
	denyCreate bool
	requests   []domain.AuthorizationRequest
}

func (a *namespaceAuthorizer) Authorize(_ context.Context, r domain.AuthorizationRequest) error {
	a.requests = append(a.requests, r)
	if a.denyCreate && r.Action == "namespace.create" {
		return fmt.Errorf("creation denied")
	}
	return nil
}
func TestCatalogPreflightNamespaceReadCreateAndPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		firstGet, post                          int
		terminating, disallowed, disabled, deny bool
		wantError                               string
		wantGets, wantPosts                     int
	}{
		{name: "existing", firstGet: 200, wantGets: 1},
		{name: "missing", firstGet: 404, post: 201, wantGets: 2, wantPosts: 1},
		{name: "forbidden-read", firstGet: 403, wantError: "NAMESPACE_UNAVAILABLE", wantGets: 1},
		{name: "timeout-read", firstGet: 504, wantError: "NAMESPACE_UNAVAILABLE", wantGets: 1},
		{name: "terminating", firstGet: 200, terminating: true, wantError: "NAMESPACE_TERMINATING", wantGets: 1},
		{name: "create-forbidden", firstGet: 404, post: 403, wantError: "NAMESPACE_CREATION_FAILED", wantGets: 1, wantPosts: 1},
		{name: "concurrent-create", firstGet: 404, post: 409, wantGets: 2, wantPosts: 1},
		{name: "concurrent-terminating", firstGet: 404, post: 409, terminating: true, wantError: "NAMESPACE_TERMINATING", wantGets: 2, wantPosts: 1},
		{name: "outside-scope", disallowed: true, wantError: "not allowed"},
		{name: "write-disabled", firstGet: 404, disabled: true, wantError: "NAMESPACE_CREATION_FAILED", wantGets: 1},
		{name: "authorizer-denied", firstGet: 404, deny: true, wantError: "NAMESPACE_CREATION_FAILED", wantGets: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gets, posts := 0, 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				status := 200
				switch r.Method {
				case http.MethodGet:
					require.Equal(t, "/api/v1/namespaces/demo", r.URL.Path)
					gets++
					if gets == 1 {
						status = tc.firstGet
					}
				case http.MethodPost:
					require.Equal(t, "/api/v1/namespaces", r.URL.Path)
					posts++
					status = tc.post
					var obj map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&obj))
					require.Equal(t, "demo", obj["metadata"].(map[string]any)["name"])
				default:
					t.Errorf("unexpected API mutation: %s %s", r.Method, r.URL.Path)
				}
				if status >= 400 {
					reason := map[int]string{403: "Forbidden", 404: "NotFound", 409: "AlreadyExists", 504: "Timeout"}[status]
					w.WriteHeader(status)
					fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":%q,"code":%d}`, reason, status)
					return
				}
				phase := "Active"
				if tc.terminating {
					phase = "Terminating"
				}
				w.WriteHeader(status)
				fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"demo"},"status":{"phase":%q}}`, phase)
			}))
			defer api.Close()
			provider := memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: t\n  cluster:\n    server: %s\ncontexts:\n- name: t\n  context:\n    cluster: t\ncurrent-context: t\n", api.URL))
			allowed := []string{"demo"}
			if tc.disallowed {
				allowed = []string{"other"}
			}
			cfg := config.KubernetesConfig{WriteEnabled: !tc.disabled, AllowedNamespaces: allowed}
			client, err := clusterk8s.New(cfg, provider)
			require.NoError(t, err)
			authorizer := &namespaceAuthorizer{denyCreate: tc.deny}
			bridge := application.CatalogCluster{Service: application.NewClusterService(client, nil, cfg, application.WithAuthorizer(authorizer))}
			err = bridge.Preflight(context.Background(), d.Instance{Namespace: "demo", Software: "nginx"}, d.Parameters{})
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.wantGets, gets)
			require.Equal(t, tc.wantPosts, posts)
			for _, r := range authorizer.requests {
				require.True(t, r.Action == "catalog.preflight" || r.Action == "namespace.create")
				require.Equal(t, "demo", r.Targets[0].Name)
			}
		})
	}
}
