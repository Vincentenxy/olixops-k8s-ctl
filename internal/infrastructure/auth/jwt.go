// Package auth adapts RSA JWTs and SQLite token metadata to authentication ports.
package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	d "olixops-k8s-ctl/internal/domain/auth"
)

type CompanyConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	Issuer        string `mapstructure:"issuer"`
	Audience      string `mapstructure:"audience"`
	KeyID         string `mapstructure:"key-id"`
	PublicKey     string `mapstructure:"public-key"`
	PublicKeyFile string `mapstructure:"public-key-file"`
}
type PATConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	Issuer         string `mapstructure:"issuer"`
	Audience       string `mapstructure:"audience"`
	KeyID          string `mapstructure:"key-id"`
	PrivateKeyFile string `mapstructure:"private-key-file"`
	PublicKeyFile  string `mapstructure:"public-key-file"`
	DatabasePath   string `mapstructure:"database-path"`
}
type Config struct {
	Enabled bool          `mapstructure:"enabled"`
	Company CompanyConfig `mapstructure:"company"`
	PAT     PATConfig     `mapstructure:"pat"`
}
type provider struct {
	issuer, audience, kid string
	key                   *rsa.PublicKey
	pat                   bool
}
type JWT struct {
	providers []provider
	private   *rsa.PrivateKey
	pat       PATConfig
}

func NewJWT(cfg Config) (*JWT, error) {
	j := &JWT{pat: cfg.PAT}
	if cfg.Company.Enabled {
		text := cfg.Company.PublicKey
		if cfg.Company.PublicKeyFile != "" {
			b, e := os.ReadFile(cfg.Company.PublicKeyFile)
			if e != nil {
				return nil, fmt.Errorf("read company public key failed")
			}
			text = string(b)
		}
		key, e := parsePublic(text)
		if e != nil {
			return nil, fmt.Errorf("invalid company public key")
		}
		if strings.TrimSpace(cfg.Company.Issuer) == "" || strings.TrimSpace(cfg.Company.Audience) == "" {
			return nil, fmt.Errorf("company issuer and audience required")
		}
		j.providers = append(j.providers, provider{cfg.Company.Issuer, cfg.Company.Audience, cfg.Company.KeyID, key, false})
	}
	if cfg.PAT.Enabled {
		p := cfg.PAT
		if p.Issuer == "" || p.Audience == "" || p.KeyID == "" || p.DatabasePath == "" {
			return nil, fmt.Errorf("PAT issuer, audience, key-id and database-path required")
		}
		raw, e := os.ReadFile(p.PrivateKeyFile)
		if e != nil {
			return nil, fmt.Errorf("read PAT private key failed")
		}
		private, e := jwt.ParseRSAPrivateKeyFromPEM(raw)
		if e != nil || private.N.BitLen() < 2048 {
			return nil, fmt.Errorf("invalid PAT private key (RSA >= 2048 required)")
		}
		pubRaw, e := os.ReadFile(p.PublicKeyFile)
		if e != nil {
			return nil, fmt.Errorf("read PAT public key failed")
		}
		pub, e := parsePublic(string(pubRaw))
		if e != nil {
			return nil, fmt.Errorf("invalid PAT public key")
		}
		if private.N.Cmp(pub.N) != 0 || private.E != pub.E {
			return nil, fmt.Errorf("PAT key pair mismatch")
		}
		j.private = private
		j.providers = append(j.providers, provider{p.Issuer, p.Audience, p.KeyID, pub, true})
	}
	if len(j.providers) == 0 {
		return nil, fmt.Errorf("enable company JWT or PAT authentication")
	}
	return j, nil
}
func parsePublic(raw string) (*rsa.PublicKey, error) {
	if !strings.Contains(raw, "-----BEGIN") {
		der, e := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
		if e != nil {
			return nil, e
		}
		raw = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	}
	key, e := jwt.ParseRSAPublicKeyFromPEM([]byte(raw))
	if e != nil {
		return nil, e
	}
	if key.N.BitLen() < 2048 {
		return nil, fmt.Errorf("RSA key too short")
	}
	return key, nil
}
func str(c jwt.MapClaims, k string) string { v, _ := c[k].(string); return strings.TrimSpace(v) }
func (j *JWT) Verify(raw string) (d.Principal, error) {
	for _, p := range j.providers {
		claims := jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
			if p.kid != "" && t.Header["kid"] != p.kid {
				return nil, d.ErrUnauthenticated
			}
			return p.key, nil
		}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(p.issuer), jwt.WithAudience(p.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second))
		if err != nil || !token.Valid {
			continue
		}
		source, use := str(claims, "authSource"), str(claims, "tokenUse")
		marker := source == d.PATSource || use == d.PATUse || token.Header["typ"] == d.PATType
		if p.pat {
			if source != d.PATSource || use != d.PATUse || token.Header["typ"] != d.PATType || str(claims, "jti") == "" {
				continue
			}
		} else if marker {
			continue
		} // Company signatures cannot mint local PAT identities.
		user := str(claims, "staffuserid")
		if user == "" {
			user = str(claims, "sub")
		}
		if user == "" {
			continue
		}
		return d.Principal{UserID: user, Name: str(claims, "name"), AuthSource: source, TokenUse: use, TokenID: str(claims, "jti")}, nil
	}
	return d.Principal{}, d.ErrUnauthenticated
}
func (j *JWT) Issue(p d.Principal, id string, expires time.Time) (string, error) {
	if j.private == nil {
		return "", d.ErrForbidden
	}
	now := time.Now().UTC()
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": j.pat.Issuer, "aud": j.pat.Audience, "sub": p.UserID, "staffuserid": p.UserID, "name": p.Name, "authSource": d.PATSource, "tokenUse": d.PATUse, "jti": id, "exp": expires.Unix(), "iat": now.Unix(), "nbf": now.Unix()})
	t.Header["kid"] = j.pat.KeyID
	t.Header["typ"] = d.PATType
	return t.SignedString(j.private)
}
