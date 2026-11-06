package application

import (
	"context"
	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/domain"
	authdomain "olixops-k8s-ctl/internal/domain/auth"
	"testing"
)

func TestDiagnosticGrantDoesNotTrustCallerTargetOrIdentity(t *testing.T) {
	s := &ClusterService{}
	WithDiagnosticGrants([]domain.DiagnosticGrant{{UserID: "alice", Namespace: "demo", Pod: "app-1", Container: "app", PodUID: "uid-1", Actions: []string{"logs", "read_file"}, Files: []string{"/app/config.yaml"}}})(s)
	ctx := authdomain.WithPrincipal(context.Background(), authdomain.Principal{UserID: "alice"})
	grant, err := s.diagnosticGrant(ctx, "demo", "app-1", "app", "read_file", "/app/config.yaml")
	require.NoError(t, err)
	require.Equal(t, "uid-1", grant.PodUID)
	for _, test := range []struct {
		ctx                              context.Context
		ns, pod, container, action, file string
	}{
		{context.Background(), "demo", "app-1", "app", "logs", ""},
		{authdomain.WithPrincipal(ctx, authdomain.Principal{UserID: "bob"}), "demo", "app-1", "app", "logs", ""},
		{ctx, "other", "app-1", "app", "logs", ""}, {ctx, "demo", "other", "app", "logs", ""}, {ctx, "demo", "app-1", "other", "logs", ""},
		{ctx, "demo", "app-1", "app", "environment", ""}, {ctx, "demo", "app-1", "app", "read_file", "/etc/shadow"},
		{ctx, "demo", "app-1", "app", "read_file", "/app/../app/config.yaml"},
	} {
		_, err = s.diagnosticGrant(test.ctx, test.ns, test.pod, test.container, test.action, test.file)
		require.Error(t, err)
	}
}
