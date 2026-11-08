// Package httpio owns network request-body lifetimes; application services only
// depend on io.ReadCloser and context, never on HTTP connection details.
package httpio

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"olixops-k8s-ctl/internal/domain/fault"
)

const JSONReadTimeout = 10 * time.Second

// watchCancellation interrupts a real network Read, which context cancellation
// or Request.Body.Close alone cannot reliably unblock on an HTTP/1 connection.
func watchCancellation(ctx context.Context, control *http.ResponseController) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = control.SetReadDeadline(time.Now())
		close(done)
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}

// ReadJSONBody limits only the body-read phase. The deadline is cleared after a
// complete read so command execution and subsequent downloads retain their budgets.
func ReadJSONBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	control := http.NewResponseController(w)
	deadline := time.Now().Add(JSONReadTimeout)
	if parent, ok := r.Context().Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	if err := control.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, fault.Unavailable("cannot set request body read deadline")
	}
	stop := watchCancellation(r.Context(), control)
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	stop()
	// net/http may cancel the request context as a consequence of a socket
	// timeout. Preserve the original read timeout instead of reporting cancel.
	var network net.Error
	if errors.As(err, &network) && network.Timeout() && !time.Now().Before(deadline) {
		err = context.DeadlineExceeded
	} else if cause := r.Context().Err(); cause != nil {
		err = cause
	}
	if err != nil {
		// Do not clear an expired deadline and let net/http drain a stalled body.
		if r.ProtoMajor == 1 {
			w.Header().Set("Connection", "close")
		}
		_ = control.SetReadDeadline(time.Now())
		_ = r.Body.Close()
		if errors.Is(err, context.Canceled) {
			return nil, &fault.Error{Reason: "canceled", Message: "Request body read canceled", Cause: err}
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
			return nil, &fault.Error{Reason: "deadline_exceeded", Message: "JSON request body read deadline exceeded", Cause: context.DeadlineExceeded}
		}
		return nil, fault.Invalid("cannot read request body or JSON size limit exceeded")
	}
	_ = r.Body.Close()
	_ = control.SetReadDeadline(time.Time{})
	return raw, nil
}

// InterruptibleBody does not impose a transfer duration or file size limit.
// Close on an unfinished upload interrupts the socket read before closing Body.
func InterruptibleBody(w http.ResponseWriter, r *http.Request) io.ReadCloser {
	control := http.NewResponseController(w)
	body := &interruptibleBody{body: r.Body, control: control, stop: watchCancellation(r.Context(), control)}
	// An empty upload may finish before the producer goroutine gets scheduled.
	// Interrupting its idle keep-alive socket would cancel a successful request.
	body.eof.Store(r.ContentLength == 0)
	return body
}

type interruptibleBody struct {
	body    io.ReadCloser
	control *http.ResponseController
	stop    func()
	eof     atomic.Bool
	once    sync.Once
	err     error
}

func (b *interruptibleBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if err == io.EOF {
		b.eof.Store(true)
	}
	return n, err
}
func (b *interruptibleBody) Close() error {
	b.once.Do(func() {
		b.stop()
		if !b.eof.Load() {
			_ = b.control.SetReadDeadline(time.Now())
		}
		b.err = b.body.Close()
	})
	return b.err
}
