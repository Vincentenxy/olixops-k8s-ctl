package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"olixops-k8s-ctl/internal/domain/fault"

	"olixops-k8s-ctl/internal/domain"
)

type ServiceOption func(*ClusterService)

// WithAuthorizer reserves a single application-layer policy seam for REST and MCP.
// The default is explicitly DeferredAuthorizer (allow), not a security policy.
func WithAuthorizer(authorizer domain.Authorizer) ServiceOption {
	return func(s *ClusterService) {
		if authorizer != nil {
			s.authorizer = authorizer
		}
	}
}

func (s *ClusterService) authorize(ctx context.Context, action string, write bool, targets []domain.Target, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if write && !s.writeEnabled {
		return fault.Disabled("kubernetes write operations are disabled")
	}
	request := domain.AuthorizationRequest{Cluster: s.cluster, Action: action, Write: write, Targets: targets}
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fault.Invalid("encode authorization payload")
		}
		digest := sha256.Sum256(data)
		request.PayloadDigest = hex.EncodeToString(digest[:])
	}
	if err := s.authorizer.Authorize(ctx, request); err != nil {
		return &fault.Error{Reason: "forbidden", Message: "Operation rejected by authorization policy", Cause: err}
	}
	return nil
}

// WithClusterID fixes the instance identity used in all authorization requests.
func WithClusterID(id string) ServiceOption {
	return func(s *ClusterService) {
		if id != "" {
			s.cluster = id
		}
	}
}
