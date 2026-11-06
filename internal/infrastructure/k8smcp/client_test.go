package k8smcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestPrivateEndpoint(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1/mcp", "http://localhost/mcp", "http://10.0.0.1/mcp", "http://127.0.0.1/other", "http://user:pass@127.0.0.1/mcp", "http://127.0.0.1/mcp?token=x"} {
		_, err := New(endpoint)
		require.Error(t, err, endpoint)
	}
	_, err := New("http://127.0.0.1:8082/mcp")
	require.NoError(t, err)
}
func TestSDKDelegation(t *testing.T) {
	for _, tc := range []struct {
		name, output      string
		failed, truncated bool
	}{
		{"success", "PONG", false, false}, {"tool error", "command unavailable", true, false}, {"bounded output", strings.Repeat("x", maxOutputBytes+1), false, true},
		{"bounded UTF-8", strings.Repeat("€", maxOutputBytes/3+1), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := mcp.NewServer(&mcp.Implementation{Name: "upstream-fixture", Version: "v0.1.2"}, nil)
			server.AddTool(&mcp.Tool{Name: "pods_exec", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				var args map[string]any
				require.NoError(t, json.Unmarshal(req.Params.Arguments, &args))
				require.Equal(t, map[string]any{"namespace": "demo", "name": "redis-0", "container": "redis", "command": []any{"redis-cli", "PING"}}, args)
				return &mcp.CallToolResult{IsError: tc.failed, Content: []mcp.Content{&mcp.TextContent{Text: tc.output}}}, nil
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Empty(t, r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("Cookie"))
				handler.ServeHTTP(w, r)
			}))
			defer upstream.Close()
			client, err := New(upstream.URL + "/mcp")
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := client.Execute(ctx, "demo", "redis-0", "redis", []string{"redis-cli", "PING"})
			require.NoError(t, err)
			require.Equal(t, int32(1), calls.Load())
			require.Equal(t, tc.failed, out.UpstreamError)
			require.Equal(t, tc.truncated, out.Truncated)
			require.LessOrEqual(t, len(out.Output), maxOutputBytes)
			if tc.failed {
				require.Equal(t, "upstream_error", out.Outcome)
			} else {
				require.Equal(t, "completed", out.Outcome)
			}
		})
	}
}
func TestUpstreamUnavailableDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer upstream.Close()
	client, err := New(upstream.URL + "/mcp")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := client.Execute(ctx, "demo", "pod", "main", []string{"ps"})
	require.Error(t, err)
	require.Equal(t, "not_executed", out.Outcome)
	// Initialization may use protocol negotiation; no pods_exec can be submitted without it.
	require.Greater(t, calls.Load(), int32(0))
}

func TestExecutionDeadlineIsUnknown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "pods_exec", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(started)
		select {
		case <-ctx.Done():
		case <-release:
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "late result"}}}, nil
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer upstream.Close()
	defer close(release) // Remote work may continue after the client cancels.
	client, err := New(upstream.URL + "/mcp")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	out, err := client.Execute(ctx, "demo", "pod", "main", []string{"ps"})
	require.NoError(t, err)
	require.True(t, out.TimedOut)
	require.Equal(t, "unknown", out.Outcome)
	select {
	case <-started:
	default:
		t.Fatal("exec call did not start")
	}
}
