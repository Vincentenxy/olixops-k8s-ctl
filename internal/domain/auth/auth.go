// Package auth defines trusted identities and personal token persistence ports.
package auth

import (
	"context"
	"errors"
	"time"
)

const PATSource = "personalToken"
const PATUse = "personalAccessToken"
const PATType = "k8s-ctl-pat+jwt"

var ErrUnauthenticated = errors.New("authentication failed")
var ErrForbidden = errors.New("operation not permitted")
var ErrInvalid = errors.New("invalid token parameters")
var ErrNotFound = errors.New("token not found")
var ErrLimit = errors.New("active token limit reached")

type Principal struct {
	UserID     string `json:"userId"`
	Name       string `json:"name"`
	AuthSource string `json:"authSource"`
	TokenUse   string `json:"tokenUse"`
	TokenID    string `json:"tokenId,omitempty"`
}
type contextKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, p)
}
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(contextKey{}).(Principal)
	return p, ok && p.UserID != ""
}

type Token struct {
	ID         string     `json:"id"`
	OwnerID    string     `json:"ownerId"`
	Name       string     `json:"name"`
	Hash       string     `json:"-"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}
type Repository interface {
	Create(context.Context, Token, int) error
	List(context.Context, string) ([]Token, error)
	Revoke(context.Context, string, string, time.Time) error
	Validate(context.Context, string, string, string, time.Time) (bool, error)
}
type Verifier interface {
	Verify(string) (Principal, error)
}
type Issuer interface {
	Issue(Principal, string, time.Time) (string, error)
}
