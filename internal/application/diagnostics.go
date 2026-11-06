package application

import (
	"context"
	"olixops-k8s-ctl/internal/domain"
	authdomain "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/internal/domain/fault"
	"slices"
)

func WithDiagnosticGrants(grants []domain.DiagnosticGrant) ServiceOption {
	return func(s *ClusterService) {
		s.diagnosticGrantsEnabled = true
		s.diagnosticGrants = append([]domain.DiagnosticGrant{}, grants...)
	}
}
func (s *ClusterService) diagnosticGrant(ctx context.Context, namespace, pod, container, action, file string) (*domain.DiagnosticGrant, error) {
	if !s.diagnosticGrantsEnabled {
		return nil, nil
	}
	principal, ok := authdomain.FromContext(ctx)
	if !ok {
		return nil, fault.Unauthenticated("diagnostic identity required")
	}
	for i := range s.diagnosticGrants {
		grant := &s.diagnosticGrants[i]
		if grant.UserID != principal.UserID || grant.Namespace != namespace || grant.Pod != pod || grant.Container != container || !slices.Contains(grant.Actions, action) {
			continue
		}
		if action == "read_file" && (!slices.Contains(grant.Files, file) || !domain.ValidDiagnosticFilePath(file)) {
			continue
		}
		return grant, nil
	}
	return nil, fault.Forbidden("diagnostic target or action not granted")
}

// DiagnosticAccessConfigured lets interfaces fail closed if wiring omitted the grant policy.
func (s *ClusterService) DiagnosticAccessConfigured() bool {
	return s.diagnosticGrantsEnabled && len(s.diagnosticGrants) > 0
}
