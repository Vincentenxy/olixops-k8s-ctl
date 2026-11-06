// Package auth provides authentication and owner-scoped personal token lifecycle.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	d "olixops-k8s-ctl/internal/domain/auth"
)

type Service struct {
	verifier d.Verifier
	issuer   d.Issuer
	repo     d.Repository
}

func New(v d.Verifier, i d.Issuer, r d.Repository) *Service { return &Service{v, i, r} }
func tokenHash(raw string) string                           { h := sha256.Sum256([]byte(raw)); return hex.EncodeToString(h[:]) }
func (s *Service) Authenticate(ctx context.Context, raw string) (d.Principal, error) {
	if err := ctx.Err(); err != nil {
		return d.Principal{}, err
	}
	p, err := s.verifier.Verify(raw)
	if err != nil {
		return d.Principal{}, d.ErrUnauthenticated
	}
	if p.TokenUse == d.PATUse {
		if s.repo == nil {
			return d.Principal{}, d.ErrUnauthenticated
		}
		active, err := s.repo.Validate(ctx, p.TokenID, p.UserID, tokenHash(raw), time.Now().UTC())
		if err != nil {
			return d.Principal{}, err
		}
		if !active {
			return d.Principal{}, d.ErrUnauthenticated
		}
	}
	return p, nil
}

type CreateInput struct {
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type CreatedToken struct {
	Metadata d.Token `json:"metadata"`
	Token    string  `json:"token"`
}

func (s *Service) Create(ctx context.Context, in CreateInput) (CreatedToken, error) {
	p, ok := d.FromContext(ctx)
	if !ok {
		return CreatedToken{}, d.ErrUnauthenticated
	}
	if p.TokenUse == d.PATUse {
		return CreatedToken{}, d.ErrForbidden
	}
	in.Name = strings.TrimSpace(in.Name)
	now := time.Now().UTC()
	in.ExpiresAt = in.ExpiresAt.UTC().Truncate(time.Second)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 || !in.ExpiresAt.After(now) || in.ExpiresAt.After(now.Add(365*24*time.Hour)) {
		return CreatedToken{}, d.ErrInvalid
	}
	if s.issuer == nil || s.repo == nil {
		return CreatedToken{}, d.ErrForbidden
	}
	id := uuid.NewString()
	raw, err := s.issuer.Issue(p, id, in.ExpiresAt)
	if err != nil {
		return CreatedToken{}, err
	}
	record := d.Token{ID: id, OwnerID: p.UserID, Name: in.Name, Hash: tokenHash(raw), CreatedAt: now, ExpiresAt: in.ExpiresAt}
	if err = s.repo.Create(ctx, record, 10); err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{Token: raw, Metadata: record}, nil
}
func (s *Service) List(ctx context.Context) ([]d.Token, error) {
	p, ok := d.FromContext(ctx)
	if !ok {
		return nil, d.ErrUnauthenticated
	}
	if s.repo == nil {
		return nil, d.ErrForbidden
	}
	return s.repo.List(ctx, p.UserID)
}
func (s *Service) Revoke(ctx context.Context, id string) error {
	p, ok := d.FromContext(ctx)
	if !ok {
		return d.ErrUnauthenticated
	}
	if _, err := uuid.Parse(id); err != nil {
		return d.ErrInvalid
	}
	if s.repo == nil {
		return d.ErrForbidden
	}
	return s.repo.Revoke(ctx, id, p.UserID, time.Now().UTC())
}
