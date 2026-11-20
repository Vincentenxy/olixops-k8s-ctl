// Package auth defines verified platform identities. PAT status and revocation
// are checked by the upstream Publish service; the independent operator only
// validates the signed credential and its claims.
package auth

import (
	"context"
	"errors"
	"time"
)

var ErrUnauthenticated = errors.New("authentication failed")
var ErrForbidden = errors.New("operation not permitted")

type Principal struct {
	UserID    string    `json:"userId"`
	StaffNo   string    `json:"staffNo,omitempty"`
	Name      string    `json:"name"`
	AuthType  string    `json:"authType"`
	TokenID   string    `json:"tokenId,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type contextKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, p)
}
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(contextKey{}).(Principal)
	return p, ok && p.UserID != ""
}

type Verifier interface {
	Verify(string) (Principal, error)
}

// PersonalTokenRecord matches Setting/Common's UTF-8 JSON authentication cache.
