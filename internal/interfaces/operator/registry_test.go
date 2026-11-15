package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func register(r *Registry, cluster, credential string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(Input{ClusterID: cluster, Kubeconfig: credential})
	q := httptest.NewRequest("POST", "/api/v1/operator/clusters/configure", strings.NewReader(string(b)))
	q.Header.Set("Authorization", "Bearer fixture-control")
	q.Header.Set("X-Request-Id", "test-request")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	return w
}
func TestRegistryAuthenticationIsolationAndDedup(t *testing.T) {
	var created, closed atomic.Int32
	r := New("fixture-control", 2, func(_ context.Context, in Input) (http.Handler, func(), error) {
		created.Add(1)
		return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			w.Header().Set("Cluster", in.ClusterID)
			_, _ = w.Write([]byte(q.URL.Path))
		}), func() { closed.Add(1) }, nil
	})
	defer r.Close()
	for _, header := range []string{"", "fixture-control", "Bearer wrong"} {
		q := httptest.NewRequest("POST", "/api/v1/operator/clusters/configure", strings.NewReader(`{}`))
		q.Header.Set("Authorization", header)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Code != 401 {
			t.Fatalf("unauthorized=%d", w.Code)
		}
	}
	for _, name := range []string{"a", "b", "a"} {
		w := register(r, name, "fixture")
		if w.Code != 200 || w.Header().Get("X-Request-Id") != "test-request" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if created.Load() != 2 {
		t.Fatal("duplicate configuration recreated runtime")
	}
	for _, name := range []string{"a", "b"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/clusters/"+name+"/api/v1/operator/capabilities", nil))
		if w.Header().Get("Cluster") != name || w.Body.String() != "/api/v1/operator/capabilities" {
			t.Fatal("cluster routing failed")
		}
	}
	if register(r, "c", "fixture").Code != 503 {
		t.Fatal("capacity ignored")
	}
	if register(r, "a", "changed").Code != 200 || closed.Load() != 1 {
		t.Fatal("replacement failed")
	}
	r.Close()
	if closed.Load() != 3 {
		t.Fatal("cleanup failed", closed.Load())
	}
	if register(r, "a", "fixture").Code != 503 {
		t.Fatal("closed registry accepted config")
	}
}
func TestRegistryRetainsActiveRuntimeUntilRequestFinishes(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var closed atomic.Int32
	r := New("fixture-control", 1, func(_ context.Context, in Input) (http.Handler, func(), error) {
		return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			if in.Kubeconfig == "old" {
				close(started)
				<-release
			}
		}), func() { closed.Add(1) }, nil
	})
	defer r.Close()
	if register(r, "a", "old").Code != 200 {
		t.Fatal("register failed")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/clusters/a/api/v1/test", nil))
	}()
	<-started
	if register(r, "a", "new").Code != 200 || closed.Load() != 0 {
		t.Fatal("active runtime closed")
	}
	close(release)
	wg.Wait()
	if closed.Load() != 1 {
		t.Fatal("retired runtime leaked")
	}
}
func TestRegistryRejectsUnknownFieldsAndInvalidTargets(t *testing.T) {
	r := New("fixture-control", 1, func(context.Context, Input) (http.Handler, func(), error) {
		t.Fatal("invalid input reached factory")
		return nil, nil, nil
	})
	for _, body := range []string{`{"clusterId":"a","kubeconfig":"x","extra":true}`, `{"clusterId":"../a","kubeconfig":"x"}`, `{"clusterId":"a","kubeconfig":""}`, `{"clusterId":"a","kubeconfig":"x"} {}`} {
		q := httptest.NewRequest("POST", "/api/v1/operator/clusters/configure", strings.NewReader(body))
		q.Header.Set("Authorization", "Bearer fixture-control")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}

func TestRegistryIgnoresLegacyNamespacesWithoutReplacingActiveRuntime(t *testing.T) {
	var created atomic.Int32
	r := New("fixture-control", 1, func(_ context.Context, in Input) (http.Handler, func(), error) {
		if in.AllowedNamespaces != nil {
			t.Fatal("legacy namespace configuration reached runtime factory")
		}
		created.Add(1)
		return http.NotFoundHandler(), nil, nil
	})
	defer r.Close()
	if register(r, "a", "fixture").Code != 200 {
		t.Fatal("registration should not require namespaces")
	}
	// Hundreds of old entries must neither limit registration nor change runtime identity.
	namespaces := make([]string, 250)
	for i := range namespaces {
		namespaces[i] = "legacy-other"
	}
	for _, legacy := range [][]string{namespaces, {}, {"changed"}} {
		body, err := json.Marshal(Input{ClusterID: "a", Kubeconfig: "fixture", AllowedNamespaces: legacy})
		if err != nil {
			t.Fatal(err)
		}
		q := httptest.NewRequest("POST", "/api/v1/operator/clusters/configure", strings.NewReader(string(body)))
		q.Header.Set("Authorization", "Bearer fixture-control")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if created.Load() != 1 {
		t.Fatal("Namespace configuration replaced runtime and would invalidate terminals")
	}
}
