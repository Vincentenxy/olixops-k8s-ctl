package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

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
		return fmt.Errorf("kubernetes write operations are disabled")
	}
	request := domain.AuthorizationRequest{Cluster: s.cluster, Action: action, Write: write, Targets: targets}
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode authorization payload")
		}
		digest := sha256.Sum256(data)
		request.PayloadDigest = hex.EncodeToString(digest[:])
	}
	return s.authorizer.Authorize(ctx, request)
}
