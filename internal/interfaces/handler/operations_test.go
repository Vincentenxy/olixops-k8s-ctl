package handler

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIncompleteJSONHonorsOperationDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var called atomic.Bool
	engine.POST("/operation", operation(30*time.Millisecond, func(context.Context, struct {
		Name string `json:"name"`
	}) (any, error) {
		called.Store(true)
		return nil, nil
	}))
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); engine.ServeHTTP(w, r) }))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "POST /operation HTTP/1.1\r\nHost: test\r\nContent-Length: 100\r\n\r\n{\"name\":")
	require.NoError(t, err)
	blocked := false
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		blocked = true
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler stuck after disconnect")
	}
	require.False(t, blocked, "JSON body read must be bounded, before invoking business logic")
	require.False(t, called.Load())
}
