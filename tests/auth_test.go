package tests

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	app "olixops-k8s-ctl/internal/application/auth"
	d "olixops-k8s-ctl/internal/domain/auth"
	infra "olixops-k8s-ctl/internal/infrastructure/auth"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/interfaces/bootstrap"
)

type authFixture struct {
	key     *rsa.PrivateKey
	patKey  *rsa.PrivateKey
	cfg     *config.Config
	service *app.Service
}

func newAuthFixture(t *testing.T) authFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	patKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	patDer, err := x509.MarshalPKIXPublicKey(&patKey.PublicKey)
	require.NoError(t, err)
	cfg := infra.Config{
		Enabled: true,
		JWT: infra.JWTConfig{
			Enabled:   true,
			Issuer:    "platform",
			Audience:  "platform-api",
			KeyID:     "platform-pat-v1",
			PublicKey: base64.StdEncoding.EncodeToString(der),
		},
		PAT: infra.PATConfig{
			Enabled:   true,
			PublicKey: base64.StdEncoding.EncodeToString(patDer),
			Issuer:    "pat-platform",
			Audience:  "pat-api",
			KeyID:     "pat-v1",
		},
	}
	service, cleanup, err := bootstrap.Authentication(cfg)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	runtime := &config.Config{
		Server: config.ServerConfig{Mode: "test"},
		MCP:    config.MCPConfig{Enabled: true},
		Auth:   cfg,
	}
	return authFixture{key: key, patKey: patKey, cfg: runtime, service: service}
}

func (f authFixture) token(t *testing.T, claims jwt.MapClaims, tokenType string) string {
	t.Helper()
	if claims == nil {
		claims = jwt.MapClaims{}
	}
	issuer, audience, keyID := "platform", "platform-api", "platform-pat-v1"
	if tokenType == "efficient-pat+jwt" {
		issuer, audience, keyID = "pat-platform", "pat-api", "pat-v1"
	}
	defaults := jwt.MapClaims{
		"iss": issuer, "aud": audience, "sub": "wx-user", "staffuserid": "wx-user",
		"staffno": "00123", "name": "测试用户", "iat": time.Now().Add(-time.Second).Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
	for key, value := range defaults {
		if _, exists := claims[key]; !exists {
			claims[key] = value
		}
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = keyID
	token.Header["typ"] = tokenType
	signingKey := f.key
	if tokenType == "efficient-pat+jwt" {
		signingKey = f.patKey
	}
	raw, err := token.SignedString(signingKey)
	require.NoError(t, err)
	return raw
}

func (f authFixture) jwt(t *testing.T, change func(jwt.MapClaims), header func(map[string]any)) string {
	claims := jwt.MapClaims{}
	if change != nil {
		change(claims)
	}
	raw := f.token(t, claims, "JWT")
	if header == nil {
		return raw
	}
	parsed, err := jwt.Parse(raw, func(token *jwt.Token) (any, error) {
		return f.key.Public(), nil
	})
	require.NoError(t, err)
	_ = parsed
	return raw
}

func (f authFixture) pat(t *testing.T, user string) string {
	return f.token(t, jwt.MapClaims{
		"sub":         user,
		"staffuserid": user,
		"authSource":  "personalToken",
		"tokenUse":    "personalAccessToken",
		"jti":         "fixture-pat-1",
	}, "efficient-pat+jwt")
}

type bearerTransport struct{ raw string }

func (b bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+b.raw)
	return http.DefaultTransport.RoundTrip(clone)
}

func TestAuthenticationRequiresIssuerAndAudience(t *testing.T) {
	base := infra.Config{Enabled: true, JWT: infra.JWTConfig{Enabled: true}}
	_, cleanup, err := bootstrap.Authentication(base)
	cleanup()
	require.ErrorContains(t, err, "issuer and audience")
}

func TestPlatformJWTTrustAndClaims(t *testing.T) {
	f := newAuthFixture(t)
	principal, err := f.service.Authenticate(context.Background(), f.token(t, nil, "JWT"))
	require.NoError(t, err)
	require.Equal(t, "wx-user", principal.UserID)
	require.Equal(t, "00123", principal.StaffNo)
	require.Equal(t, "测试用户", principal.Name)
	require.Equal(t, "JWT", principal.AuthType)

	for name, claims := range map[string]jwt.MapClaims{
		"wrong issuer":       {"iss": "other"},
		"wrong audience":     {"aud": "other"},
		"missing user":       {"staffuserid": nil},
		"partial PAT marker": {"authSource": "personalToken"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.service.Authenticate(context.Background(), f.token(t, claims, "JWT"))
			require.ErrorIs(t, err, d.ErrUnauthenticated)
		})
	}
}

func TestPlatformPATUsesSignedClaimsWithoutRedis(t *testing.T) {
	f := newAuthFixture(t)
	raw := f.token(t, jwt.MapClaims{
		"authSource": "personalToken",
		"tokenUse":   "personalAccessToken",
		"jti":        "token-1",
	}, "efficient-pat+jwt")
	principal, err := f.service.Authenticate(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, "PAT", principal.AuthType)
	require.Equal(t, "token-1", principal.TokenID)
	require.Equal(t, "wx-user", principal.UserID)

	for name, change := range map[string]func(jwt.MapClaims){
		"token use": func(c jwt.MapClaims) { c["tokenUse"] = "other" },
		"token id":  func(c jwt.MapClaims) { c["jti"] = "" },
		"subject":   func(c jwt.MapClaims) { c["sub"] = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			claims := jwt.MapClaims{"authSource": "personalToken", "tokenUse": "personalAccessToken", "jti": "token-1"}
			change(claims)
			_, err := f.service.Authenticate(context.Background(), f.token(t, claims, "efficient-pat+jwt"))
			require.ErrorIs(t, err, d.ErrUnauthenticated)
		})
	}
}

func TestAuthenticationRejectsAlgorithmMismatch(t *testing.T) {
	f := newAuthFixture(t)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "platform", "aud": "platform-api", "staffuserid": "user-1", "staffno": "00123", "name": "测试用户", "exp": time.Now().Add(time.Hour).Unix(),
	})
	raw, err := token.SignedString([]byte("fixture"))
	require.NoError(t, err)
	_, err = f.service.Authenticate(context.Background(), raw)
	require.ErrorIs(t, err, d.ErrUnauthenticated)
}
