package tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/domain"
	authdomain "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/internal/infrastructure/config"
	k8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

// Namespace permissions are delegated to the existing authorizer, not static lists.
func TestExecNamespaceAuthorizationUsesPolicyInsteadOfConfiguration(t *testing.T) {
	for _, namespace := range []string{"setting-efficient", "service-two-dev", "default", "kube-system", "olixops"} {
		t.Run(namespace, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/namespaces/"+namespace+"/pods/app", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"app","namespace":%q,"uid":"uid-1","labels":{"k8s-ctl/exec-enabled":"true"}},"spec":{"containers":[{"name":"app"}]},"status":{"containerStatuses":[{"name":"app","state":{"running":{}}}]}}`, namespace)
			}))
			defer api.Close()
			cfg := config.KubernetesConfig{WriteEnabled: true, AllowedNamespaces: []string{"legacy-other"}}
			client, err := k8s.New(cfg, memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: test\n  context:\n    cluster: test\ncurrent-context: test\n", api.URL)))
			require.NoError(t, err)
			backend, policy := &countingExecutor{}, &execPolicy{}
			service := application.NewClusterService(client, nil, cfg, application.WithPodExecutor(backend), application.WithAuthorizer(policy))
			ctx := authdomain.WithPrincipal(context.Background(), authdomain.Principal{UserID: "developer"})
			input := domain.PodExecInput{Namespace: namespace, Pod: "app", Container: "app", Action: "redis_ping"}
			result, err := service.ExecPodCommand(ctx, input)
			require.NoError(t, err)
			require.Equal(t, "completed", result.Outcome)
			require.Equal(t, "developer", policy.user)
			require.Equal(t, int32(1), backend.calls.Load())
			policy.deny = true
			_, err = service.ExecPodCommand(ctx, input)
			require.ErrorContains(t, err, "Operation rejected by authorization policy")
			require.Equal(t, int32(1), backend.calls.Load(), "denied requests must not execute commands")
			_, err = service.ExecPodCommand(context.Background(), input)
			require.ErrorContains(t, err, "authenticated identity required")
		})
	}
}
