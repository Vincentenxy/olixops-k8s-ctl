// Package operator exposes a trusted platform configuration entry and isolated cluster routers.
package operator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
	d "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/pkg/logger"
)

type Input struct {
	ClusterID  string `json:"clusterId"`
	Kubeconfig string `json:"kubeconfig"`
	// Deprecated: accepted for older callers, ignored and excluded from runtime identity.
	AllowedNamespaces []string `json:"allowedNamespaces,omitempty"`
}
type Factory func(context.Context, Input) (http.Handler, func(), error)
type entry struct {
	handler http.Handler
	close   func()
	digest  [32]byte
	active  int
	retired bool
}
type Registry struct {
	mu      sync.Mutex
	entries map[string]*entry
	max     int
	factory Factory
	closed  bool
}

func New(max int, factory Factory) *Registry {
	return &Registry{entries: map[string]*entry{}, max: max, factory: factory}
}
func result(w http.ResponseWriter, status int, reason string, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	code := 0
	if status >= 400 {
		code = status
		data = map[string]string{"reason": reason}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": reason, "data": data})
}

var clusterID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func (r *Registry) ServeHTTP(w http.ResponseWriter, q *http.Request) {
	id := q.Header.Get("X-Request-Id")
	if len(id) == 0 || len(id) > 128 || strings.ContainsAny(id, "\r\n") {
		id = uuid.NewString()
	}
	w.Header().Set("X-Request-Id", id)
	q = q.WithContext(context.WithValue(q.Context(), logger.TraceIDKey, id))
	if q.Method == http.MethodGet && (q.URL.Path == "/api/v1/pub/health" || q.URL.Path == "/api/v1/pub/ready") {
		result(w, 200, "", map[string]any{"operator": true})
		return
	}
	if _, ok := d.FromContext(q.Context()); !ok {
		result(w, 401, "unauthenticated", nil)
		return
	}
	if q.Method == http.MethodGet && q.URL.Path == "/api/v1/auth/me" {
		principal, _ := d.FromContext(q.Context())
		result(w, 200, "", principal)
		return
	}

	if q.URL.Path == "/api/v1/operator/clusters/configure" {
		if q.Method != http.MethodPost {
			result(w, 405, "method_not_allowed", nil)
			return
		}
		r.configure(w, q)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(q.URL.Path, "/"), "/", 3)
	if len(parts) != 3 || parts[0] != "clusters" || !clusterID.MatchString(parts[1]) {
		result(w, 404, "not_found", nil)
		return
	}
	r.mu.Lock()
	e := r.entries[parts[1]]
	if e == nil || r.closed {
		r.mu.Unlock()
		result(w, 404, "cluster_not_configured", nil)
		return
	}
	e.active++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		e.active--
		closeNow := e.retired && e.active == 0
		r.mu.Unlock()
		if closeNow {
			e.close()
		}
	}()
	clone := q.Clone(q.Context())
	u := *q.URL
	u.Path = "/" + parts[2]
	u.RawPath = ""
	clone.URL = &u
	e.handler.ServeHTTP(w, clone)
}
func (r *Registry) configure(w http.ResponseWriter, q *http.Request) {
	var in Input
	decoder := json.NewDecoder(http.MaxBytesReader(w, q.Body, 256*1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || !clusterID.MatchString(in.ClusterID) || len(in.Kubeconfig) == 0 || len(in.Kubeconfig) > 128*1024 {
		result(w, 400, "invalid_argument", nil)
		return
	}
	in.AllowedNamespaces = nil
	serialized, _ := json.Marshal(in)
	digest := sha256.Sum256(serialized)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		result(w, 503, "operator_closed", nil)
		return
	}
	old := r.entries[in.ClusterID]
	if old != nil && old.digest == digest {
		r.mu.Unlock()
		result(w, 200, "", map[string]string{"clusterId": in.ClusterID})
		return
	}
	if old == nil && len(r.entries) >= r.max {
		r.mu.Unlock()
		result(w, 503, "capacity_exceeded", nil)
		return
	}
	handler, cleanup, err := r.factory(q.Context(), in)
	if err != nil {
		r.mu.Unlock()
		result(w, 400, "invalid_cluster_configuration", nil)
		return
	}
	if cleanup == nil {
		cleanup = func() {}
	}
	r.entries[in.ClusterID] = &entry{handler: handler, close: cleanup, digest: digest}
	closeOld := old != nil && old.active == 0
	if old != nil {
		old.retired = true
	}
	r.mu.Unlock()
	if closeOld {
		old.close()
	}
	result(w, 200, "", map[string]string{"clusterId": in.ClusterID})
}
func (r *Registry) Close() {
	r.mu.Lock()
	r.closed = true
	var cleanup []func()
	for _, e := range r.entries {
		e.retired = true
		if e.active == 0 {
			cleanup = append(cleanup, e.close)
		}
	}
	r.entries = map[string]*entry{}
	r.mu.Unlock()
	for _, close := range cleanup {
		close()
	}
}
