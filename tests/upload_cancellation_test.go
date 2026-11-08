package tests

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"olixops-k8s-ctl/internal/application"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"olixops-k8s-ctl/internal/domain"
	auth "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/interfaces/router"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type reviewSignalBody struct {
	io.ReadCloser
	reads   int
	waiting chan struct{}
}

func (b *reviewSignalBody) Read(p []byte) (int, error) {
	b.reads++
	if b.reads == 2 {
		close(b.waiting)
	}
	return b.ReadCloser.Read(p)
}

type reviewFailedReceive struct {
	waiting, failed chan struct{}
	cleaned         atomic.Bool
}

func (b *reviewFailedReceive) Stream(ctx context.Context, _, _, _ string, argv []string, _ bool, in io.Reader, _, _ io.Writer) error {
	if strings.Contains(argv[2], "head -c") {
		var data [1]byte
		if _, err := io.ReadFull(in, data[:]); err != nil {
			return err
		}
		select {
		case <-b.waiting:
		case <-ctx.Done():
			return ctx.Err()
		}
		time.Sleep(30 * time.Millisecond)
		close(b.failed)
		return fmt.Errorf("remote receive failed")
	}
	if strings.HasPrefix(argv[2], "rm -f") {
		b.cleaned.Store(true)
	}
	return nil
}
func TestUploadFailureUnblocksStalledBody(t *testing.T) {
	f := newAuthFixture(t)
	f.cfg.MCP.Enabled = false
	f.cfg.Kubernetes = config.KubernetesConfig{InCluster: true, WriteEnabled: true, AllowedNamespaces: []string{"demo"}}
	f.cfg.Shell.Enabled = true
	f.cfg.PodExec = config.PodExecConfig{Enabled: true, AllowedNamespaces: []string{"demo"}}
	upstream := operatorPodAPI(t)
	defer upstream.Close()
	manager := sessions.NewManager(time.Minute, time.Hour, 2, 1)
	defer manager.Close()
	backend := &reviewFailedReceive{waiting: make(chan struct{}), failed: make(chan struct{})}
	service := application.NewClusterService(operatorClient(t, f.cfg.Kubernetes, upstream.URL), nil, f.cfg.Kubernetes,
		application.WithPodExecutor(&countingExecutor{}, []string{"demo"}), application.WithShell(true, true, interceptor.AllowAll{}), application.WithTerminals(backend, manager))
	ticket, err := service.UploadPodFile(auth.WithPrincipal(context.Background(), auth.Principal{UserID: "wx-user"}), domain.UploadInput{TerminalTarget: domain.TerminalTarget{Namespace: "demo", Pod: "app", Container: "app"}, Path: "/tmp/file.bin"})
	require.NoError(t, err)
	engine, err := router.NewWithAuth(f.cfg, service, nil, f.service)
	require.NoError(t, err)
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = &reviewSignalBody{ReadCloser: r.Body, waiting: backend.waiting}
		defer close(handlerDone)
		engine.ServeHTTP(w, r)
	}))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/octet-stream\r\nX-Upload-Id: %s\r\nContent-Length: 1048576\r\n\r\nA", application.UploadRoute, server.Listener.Addr().String(), f.company(t, nil, nil), ticket.UploadID)
	require.NoError(t, err)
	select {
	case <-backend.failed:
	case <-time.After(3 * time.Second):
		t.Fatal("did not reach failed receiver")
	}
	blocked := false
	select {
	case <-handlerDone:
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	cleanedBeforeClose := backend.cleaned.Load()
	conn.Close()
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not recover after disconnect")
	}
	t.Logf("after receiver failure: handlerBlocked=%v cleanupRan=%v; after disconnect cleanup=%v", blocked, cleanedBeforeClose, backend.cleaned.Load())
	require.False(t, blocked, "remote failure must interrupt the HTTP body without a client disconnect")
	require.True(t, cleanedBeforeClose, "staging cleanup must run before client disconnects")
}
