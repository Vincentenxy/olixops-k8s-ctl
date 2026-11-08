package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"testing"
	"time"
)

func TestMCPIncompleteBodyDeadline(t *testing.T) {
	h := NewHandler(application.NewClusterService(nil, nil, config.KubernetesConfig{}), "test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Millisecond)
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(2*time.Second)))
	_, err = fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: test\r\nAccept: application/json, text/event-stream\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"jsonrpc\":")
	require.NoError(t, err)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 504, response.StatusCode)
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	var message map[string]any
	require.NoError(t, json.Unmarshal(data, &message))
	require.Equal(t, "2.0", message["jsonrpc"])
	require.Contains(t, message, "error")
	require.NotContains(t, message, "code")
	require.Contains(t, string(data), "deadline_exceeded")
}
