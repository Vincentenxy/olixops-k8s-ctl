package tests

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application"
	app "olixops-k8s-ctl/internal/application/auth"
	"olixops-k8s-ctl/internal/domain"
	d "olixops-k8s-ctl/internal/domain/auth"
	infra "olixops-k8s-ctl/internal/infrastructure/auth"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/interfaces/router"
)

type authFixture struct {
	key     *rsa.PrivateKey
	cfg     *config.Config
	adapter *infra.JWT
	repo    *infra.Store
	service *app.Service
}

func newAuthFixture(t *testing.T) authFixture {
	t.Helper()
	dir := t.TempDir()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, e)
	priv := filepath.Join(dir, "private.pem")
	pub := filepath.Join(dir, "public.pem")
	require.NoError(t, os.WriteFile(priv, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
	der, e := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(pub, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0600))
	cfg := &config.Config{Server: config.ServerConfig{Mode: "test"}, MCP: config.MCPConfig{Enabled: true}, Auth: infra.Config{Enabled: true, Company: infra.CompanyConfig{Enabled: true, Issuer: "company", Audience: "k8s-ctl", KeyID: "company-v1", PublicKey: base64.StdEncoding.EncodeToString(der)}, PAT: infra.PATConfig{Enabled: true, Issuer: "k8s-ctl", Audience: "k8s-ctl", KeyID: "pat-v1", PrivateKeyFile: priv, PublicKeyFile: pub, DatabasePath: filepath.Join(dir, "auth.db")}}}
	adapter, e := infra.NewJWT(cfg.Auth)
	require.NoError(t, e)
	repo, e := infra.Open(cfg.Auth.PAT.DatabasePath)
	require.NoError(t, e)
	t.Cleanup(func() { repo.Close() })
	return authFixture{key, cfg, adapter, repo, app.New(adapter, adapter, repo)}
}
func (f authFixture) company(t *testing.T, change func(jwt.MapClaims), header func(map[string]any)) string {
	t.Helper()
	claims := jwt.MapClaims{"iss": "company", "aud": "k8s-ctl", "sub": "fallback", "staffuserid": "wx-user", "name": "测试用户", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
	if change != nil {
		change(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "company-v1"
	if header != nil {
		header(token.Header)
	}
	raw, e := token.SignedString(f.key)
	require.NoError(t, e)
	return raw
}
func TestJWTTrustAndClaims(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	p, e := f.service.Authenticate(ctx, f.company(t, nil, nil))
	require.NoError(t, e)
	require.Equal(t, "wx-user", p.UserID)
	_, e = f.service.Authenticate(ctx, f.company(t, func(c jwt.MapClaims) { delete(c, "staffuserid") }, nil))
	require.NoError(t, e)
	for name, change := range map[string]func(jwt.MapClaims){"issuer": func(c jwt.MapClaims) { c["iss"] = "other" }, "audience": func(c jwt.MapClaims) { c["aud"] = "other" }, "expired": func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }, "missing expiry": func(c jwt.MapClaims) { delete(c, "exp") }, "future": func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Minute).Unix() }, "future issued": func(c jwt.MapClaims) { c["iat"] = time.Now().Add(time.Minute).Unix() }, "identity": func(c jwt.MapClaims) { delete(c, "sub"); delete(c, "staffuserid") }, "company PAT forgery": func(c jwt.MapClaims) {
		c["authSource"] = d.PATSource
		c["tokenUse"] = d.PATUse
		c["jti"] = uuid.NewString()
	}} {
		t.Run(name, func(t *testing.T) {
			_, e := f.service.Authenticate(ctx, f.company(t, change, nil))
			require.ErrorIs(t, e, d.ErrUnauthenticated)
		})
	}
	_, e = f.service.Authenticate(ctx, f.company(t, nil, func(h map[string]any) { h["kid"] = "other" }))
	require.ErrorIs(t, e, d.ErrUnauthenticated)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "company", "aud": "k8s-ctl", "sub": "wx-user", "exp": time.Now().Add(time.Hour).Unix()})
	raw, e := token.SignedString([]byte("test-secret"))
	require.NoError(t, e)
	_, e = f.service.Authenticate(ctx, raw)
	require.ErrorIs(t, e, d.ErrUnauthenticated)
	raw = f.company(t, nil, nil)
	_, e = f.service.Authenticate(ctx, raw[:len(raw)-10]+"0000000000")
	require.ErrorIs(t, e, d.ErrUnauthenticated)
}
func TestPATLifecycleAndPersistence(t *testing.T) {
	f := newAuthFixture(t)
	ctx := d.WithPrincipal(context.Background(), d.Principal{UserID: "owner"})
	created, e := f.service.Create(ctx, app.CreateInput{Name: "client", ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, e)
	p, e := f.service.Authenticate(ctx, created.Token)
	require.NoError(t, e)
	require.Equal(t, "owner", p.UserID)
	require.Equal(t, d.PATUse, p.TokenUse)
	_, e = f.service.Create(d.WithPrincipal(ctx, p), app.CreateInput{Name: "chain", ExpiresAt: time.Now().Add(time.Hour)})
	require.ErrorIs(t, e, d.ErrForbidden)
	items, e := f.service.List(ctx)
	require.NoError(t, e)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].LastUsedAt)
	b, e := json.Marshal(items)
	require.NoError(t, e)
	require.NotContains(t, string(b), created.Token)
	require.NotContains(t, string(b), "hash")
	other := d.WithPrincipal(context.Background(), d.Principal{UserID: "other"})
	items, e = f.service.List(other)
	require.NoError(t, e)
	require.Empty(t, items)
	require.ErrorIs(t, f.service.Revoke(other, created.Metadata.ID), d.ErrNotFound)
	// Reopening the same database does not invalidate active tokens.
	second, e := infra.Open(f.cfg.Auth.PAT.DatabasePath)
	require.NoError(t, e)
	s := app.New(f.adapter, f.adapter, second)
	_, e = s.Authenticate(ctx, created.Token)
	require.NoError(t, e)
	require.NoError(t, second.Close())
	// Valid local signatures alone do not bypass the ledger or PAT type checks.
	unsigned, e := f.adapter.Issue(d.Principal{UserID: "owner"}, uuid.NewString(), time.Now().Add(time.Hour))
	require.NoError(t, e)
	_, e = f.service.Authenticate(ctx, unsigned)
	require.ErrorIs(t, e, d.ErrUnauthenticated)
	for _, change := range []func(jwt.MapClaims, map[string]any){
		func(c jwt.MapClaims, h map[string]any) { delete(c, "authSource") },
		func(c jwt.MapClaims, h map[string]any) { h["typ"] = "JWT" },
		func(c jwt.MapClaims, h map[string]any) { delete(c, "tokenUse") },
	} {
		claims := jwt.MapClaims{"iss": "k8s-ctl", "aud": "k8s-ctl", "sub": "owner", "exp": time.Now().Add(time.Hour).Unix(), "authSource": d.PATSource, "tokenUse": d.PATUse, "jti": created.Metadata.ID}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["typ"] = d.PATType
		token.Header["kid"] = "pat-v1"
		change(claims, token.Header)
		raw, e := token.SignedString(f.key)
		require.NoError(t, e)
		_, e = f.service.Authenticate(ctx, raw)
		require.ErrorIs(t, e, d.ErrUnauthenticated)
	}
	require.NoError(t, f.service.Revoke(ctx, created.Metadata.ID))
	_, e = f.service.Authenticate(ctx, created.Token)
	require.ErrorIs(t, e, d.ErrUnauthenticated)
	_, e = f.service.Create(ctx, app.CreateInput{Name: "bad", ExpiresAt: time.Now().Add(366 * 24 * time.Hour)})
	require.ErrorIs(t, e, d.ErrInvalid)
	// Persisted expiry is enforced without JWT clock leeway.
	require.NoError(t, f.repo.Create(ctx, d.Token{ID: "expired", OwnerID: "owner", Hash: "h", CreatedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(-time.Second)}, 10))
	ok, e := f.repo.Validate(ctx, "expired", "owner", "h", time.Now())
	require.NoError(t, e)
	require.False(t, ok)
}
func TestPATConcurrentLimit(t *testing.T) {
	f := newAuthFixture(t)
	ctx := d.WithPrincipal(context.Background(), d.Principal{UserID: "owner"})
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for n := 0; n < 15; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.service.Create(ctx, app.CreateInput{Name: "client", ExpiresAt: time.Now().Add(time.Hour)})
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				success++
			} else {
				require.ErrorIs(t, e, d.ErrLimit)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 10, success)
}

type authObserver struct {
	mu    sync.Mutex
	users []string
}

func (a *authObserver) Authorize(ctx context.Context, _ domain.AuthorizationRequest) error {
	p, ok := d.FromContext(ctx)
	if !ok {
		return fmt.Errorf("missing trusted principal")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.users = append(a.users, p.UserID)
	return nil
}

type bearerTransport struct{ raw string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.raw)
	return http.DefaultTransport.RoundTrip(r)
}
func TestAuthenticatedRESTAndMCP(t *testing.T) {
	f := newAuthFixture(t)
	observer := &authObserver{}
	cluster := application.NewClusterService(nil, nil, f.cfg.Kubernetes, application.WithAuthorizer(observer))
	engine, e := router.NewWithAuth(f.cfg, cluster, nil, f.service)
	require.NoError(t, e)
	request := func(method, path, raw, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if raw != "" {
			r.Header.Set("Authorization", "Bearer "+raw)
		}
		r.Header.Set("X-Olixops-Control-Key", "legacy-key")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}
	require.Equal(t, 200, request("GET", "/api/v1/pub/health", "", "").Code)
	require.Equal(t, 401, request("GET", "/api/v1/cluster/status", "", "").Code)
	require.Equal(t, 401, request("POST", "/api/v1/mcp", "", `{}`).Code)
	require.Equal(t, 404, request("POST", "/api/v1/pub/mcp", "", `{}`).Code)
	company := f.company(t, nil, nil)
	who := request("GET", "/api/v1/auth/me", company, "")
	require.Equal(t, 200, who.Code)
	require.Contains(t, who.Body.String(), "wx-user")
	require.NotEmpty(t, who.Header().Get("X-Request-Id"))
	create := request("POST", "/api/v1/user/token/create", company, fmt.Sprintf(`{"name":"mcp","expiresAt":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)))
	require.Equal(t, 200, create.Code)
	var result struct {
		Data app.CreatedToken `json:"data"`
	}
	require.NoError(t, json.Unmarshal(create.Body.Bytes(), &result))
	pat := result.Data.Token
	require.NotEmpty(t, pat)
	require.Equal(t, "no-store", create.Header().Get("Cache-Control"))
	require.Equal(t, 403, request("POST", "/api/v1/user/token/create", pat, fmt.Sprintf(`{"name":"no","expiresAt":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))).Code)
	server := httptest.NewServer(engine)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "auth-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport{pat}}}, nil)
	require.NoError(t, e)
	defer session.Close()
	out, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "validate_manifest", Arguments: map[string]any{"namespace": "demo", "manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n  namespace: demo\n"}})
	require.NoError(t, e)
	require.False(t, out.IsError)
	observer.mu.Lock()
	require.Contains(t, observer.users, "wx-user")
	observer.mu.Unlock()
	require.Equal(t, 200, request("POST", "/api/v1/user/token/delete", company, fmt.Sprintf(`{"id":%q}`, result.Data.Metadata.ID)).Code)
	require.Equal(t, 401, request("GET", "/api/v1/auth/me", pat, "").Code)
	_, e = session.ListTools(ctx, nil)
	require.Error(t, e) // Existing stateless clients re-authenticate every request.
	_, e = router.NewWithAuth(f.cfg, cluster, nil, nil)
	require.Error(t, e)
}
func TestPATConfigFailsClosed(t *testing.T) {
	f := newAuthFixture(t)
	// PAT-only deployments work before company JWT integration.
	patOnly := f.cfg.Auth
	patOnly.Company.Enabled = false
	_, e := infra.NewJWT(patOnly)
	require.NoError(t, e)
	wrongKey, e := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, e)
	wrongPath := filepath.Join(t.TempDir(), "wrong.pem")
	require.NoError(t, os.WriteFile(wrongPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(wrongKey)}), 0600))
	mismatch := f.cfg.Auth
	mismatch.PAT.PrivateKeyFile = wrongPath
	_, e = infra.NewJWT(mismatch)
	require.ErrorContains(t, e, "mismatch")
	f.cfg.Auth.PAT.PrivateKeyFile = "missing"
	_, e = infra.NewJWT(f.cfg.Auth)
	require.Error(t, e)
	cfg := infra.Config{Enabled: true}
	_, e = infra.NewJWT(cfg)
	require.Error(t, e)
	// Company-only mode keeps PAT management unavailable, without a nil-store panic.
	f.cfg.Auth.PAT.Enabled = false
	j, e := infra.NewJWT(f.cfg.Auth)
	require.NoError(t, e)
	s := app.New(j, j, nil)
	ctx := d.WithPrincipal(context.Background(), d.Principal{UserID: "owner"})
	_, e = s.List(ctx)
	require.ErrorIs(t, e, d.ErrForbidden)
}

func TestAuthenticationHeaderAndDependencyFailure(t *testing.T) {
	f := newAuthFixture(t)
	engine, e := router.NewWithAuth(f.cfg, application.NewClusterService(nil, nil, f.cfg.Kubernetes), nil, f.service)
	require.NoError(t, e)
	valid := f.company(t, nil, nil)
	for _, headers := range [][]string{nil, {"Basic " + valid}, {"Bearer"}, {"Bearer " + valid + " extra"}, {"Bearer " + valid, "Bearer " + valid}} {
		r := httptest.NewRequest("GET", "/api/v1/auth/me?token="+valid, nil)
		r.Header.Set("X-User-Id", "forged")
		r.Header.Set("Cookie", "token="+valid)
		for _, h := range headers {
			r.Header.Add("Authorization", h)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		require.Equal(t, 401, w.Code)
		require.NotEmpty(t, w.Header().Get("WWW-Authenticate"))
		require.NotContains(t, w.Body.String(), valid)
	}
	ctx := d.WithPrincipal(context.Background(), d.Principal{UserID: "owner"})
	pat, e := f.service.Create(ctx, app.CreateInput{Name: "client", ExpiresAt: time.Now().Add(time.Hour)})
	require.NoError(t, e)
	require.NoError(t, f.repo.Close())
	r := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	r.Header.Set("Authorization", "Bearer "+pat.Token)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	require.Equal(t, 503, w.Code)
	require.NotContains(t, w.Body.String(), "database")
}
