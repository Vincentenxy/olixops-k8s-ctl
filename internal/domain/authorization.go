package domain

import "context"

// Target identifies a configured Kubernetes target, never a caller-supplied connection.
type Target struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
}

// AuthorizationRequest is the seam for future policy adapters. Caller identity must
// come from trusted middleware via ctx, never from MCP arguments. PayloadDigest
// can bind future approvals to the exact requested change without logging it.
type AuthorizationRequest struct {
	Cluster       string
	Action        string
	Write         bool
	Targets       []Target
	PayloadDigest string
}

type Authorizer interface {
	Authorize(context.Context, AuthorizationRequest) error
}

// DeferredAuthorizer intentionally performs NO authentication or authorization.
// Replace it when policy is implemented. Scope checks and the write switch are
// application invariants and remain active independently of this placeholder.
type DeferredAuthorizer struct{}

func (DeferredAuthorizer) Authorize(context.Context, AuthorizationRequest) error { return nil }
