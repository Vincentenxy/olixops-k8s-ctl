// Package auth authenticates platform JWTs and signed platform personal tokens.
package auth

import (
	"context"
	"fmt"
	d "olixops-k8s-ctl/internal/domain/auth"
)

type Service struct {
	verifier   d.Verifier
	jwtEnabled bool
	patEnabled bool
}

func New(v d.Verifier, jwtEnabled bool, patEnabled bool) (*Service, error) {
	if v == nil {
		return nil, fmt.Errorf("JWT verifier required")
	}
	if !jwtEnabled && !patEnabled {
		return nil, fmt.Errorf("enable JWT or platform PAT authentication")
	}
	return &Service{verifier: v, jwtEnabled: jwtEnabled, patEnabled: patEnabled}, nil
}
func (s *Service) Authenticate(ctx context.Context, raw string) (d.Principal, error) {
	if err := ctx.Err(); err != nil {
		return d.Principal{}, err
	}
	principal, err := s.verifier.Verify(raw)
	if err != nil {
		return d.Principal{}, d.ErrUnauthenticated
	}
	if principal.AuthType == "PAT" && !s.patEnabled {
		return d.Principal{}, d.ErrUnauthenticated
	}
	if principal.AuthType == "JWT" && !s.jwtEnabled {
		return d.Principal{}, d.ErrUnauthenticated
	}
	return principal, nil
}
