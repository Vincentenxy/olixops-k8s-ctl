package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	d "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/pkg/logger"
	"olixops-k8s-ctl/pkg/response"
)

type Authenticator interface {
	Authenticate(context.Context, string) (d.Principal, error)
}
type verifiedRequest struct{}

func authenticate(request *http.Request, s Authenticator) (context.Context, int) {
	if request.Context().Value(verifiedRequest{}) == true {
		return request.Context(), 0
	}
	headers := request.Header.Values("Authorization")
	var parts []string
	if len(headers) == 1 {
		parts = strings.Fields(headers[0])
	}
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 16384 || strings.Contains(parts[1], ",") {
		return nil, 401
	}
	p, err := s.Authenticate(request.Context(), parts[1])
	if err != nil {
		if errors.Is(err, d.ErrUnauthenticated) {
			return nil, 401
		}
		return nil, 503
	}
	ctx := d.WithPrincipal(request.Context(), p)
	return context.WithValue(ctx, verifiedRequest{}, true), 0
}
func logFailure(request *http.Request, status int) {
	logger.Error(request.Context(), "platform authentication rejected", zap.Int("status", status), zap.String("method", request.Method), zap.String("path", request.URL.Path))
}

// Authenticate shares verification with the operator's outer HTTP adapter.
func Authenticate(s Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, status := authenticate(c.Request, s)
		if status != 0 {
			logFailure(c.Request, status)
			if status == 401 {
				c.Header("WWW-Authenticate", `Bearer realm="k8s-ctl"`)
			}
			response.AbortWithHTTPStatus(c, status)
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// AuthenticateHTTP protects registration as well as per-cluster routes before dispatch.
func AuthenticateHTTP(s Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" || len(id) > 128 || strings.ContainsAny(id, "\r\n") {
			id = generateID()
		}
		w.Header().Set(RequestIDHeader, id)
		r = r.Clone(r.Context())
		r.Header.Set(RequestIDHeader, id)
		r = r.WithContext(context.WithValue(r.Context(), logger.TraceIDKey, id))
		if r.Method == http.MethodGet && (r.URL.Path == "/api/v1/pub/health" || r.URL.Path == "/api/v1/pub/ready") {
			next.ServeHTTP(w, r)
			return
		}
		ctx, status := authenticate(r, s)
		if status != 0 {
			logFailure(r, status)
			if status == 401 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="k8s-ctl"`)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(status)
			reason := "unauthenticated"
			if status == 503 {
				reason = "upstream_unavailable"
			}
			_ = json.NewEncoder(w).Encode(response.Response{Code: status, Message: http.StatusText(status), Data: map[string]string{"reason": reason}})
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
