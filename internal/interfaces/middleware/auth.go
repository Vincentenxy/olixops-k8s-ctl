package middleware

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

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
		started := time.Now()
		out := &accessResponseWriter{ResponseWriter: w, status: http.StatusOK}
		id := r.Header.Get(RequestIDHeader)
		if id == "" || len(id) > 128 || strings.ContainsAny(id, "\r\n") {
			id = generateID()
		}
		w.Header().Set(RequestIDHeader, id)
		r = r.Clone(r.Context())
		r.Header.Set(RequestIDHeader, id)
		r = r.WithContext(context.WithValue(r.Context(), logger.TraceIDKey, id))
		defer func() {
			logger.Info(
				r.Context(),
				"http access",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", out.status),
				zap.Int64("bytes", out.bytes),
				zap.Duration("cost", time.Since(started)),
				zap.String("clientIp", r.RemoteAddr),
			)
		}()
		if r.Method == http.MethodGet && (r.URL.Path == "/api/v1/pub/health" || r.URL.Path == "/api/v1/pub/ready") {
			next.ServeHTTP(out, r)
			return
		}
		ctx, status := authenticate(r, s)
		if status != 0 {
			logFailure(r, status)
			if status == 401 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="k8s-ctl"`)
			}
			out.Header().Set("Content-Type", "application/json")
			out.Header().Set("Cache-Control", "no-store")
			out.WriteHeader(status)
			reason := "unauthenticated"
			if status == 503 {
				reason = "upstream_unavailable"
			}
			_ = json.NewEncoder(out).Encode(response.Response{Code: status, Message: http.StatusText(status), Data: map[string]string{"reason": reason}})
			return
		}
		next.ServeHTTP(out, r.WithContext(ctx))
	})
}

// accessResponseWriter records the outer HTTP result without breaking streaming,
// hijacking or server push used by file and terminal endpoints.
type accessResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *accessResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *accessResponseWriter) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(payload)
	w.bytes += int64(n)
	return n, err
}

func (w *accessResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *accessResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *accessResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (w *accessResponseWriter) ReadFrom(source io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(source)
		w.bytes += n
		return n, err
	}
	n, err := io.Copy(w.ResponseWriter, source)
	w.bytes += n
	return n, err
}
