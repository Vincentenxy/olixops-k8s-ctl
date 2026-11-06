// Package k8smcp delegates container execution to a private, pinned MCP sidecar.
package k8smcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"olixops-k8s-ctl/internal/domain"
	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
)

const maxResponseBytes int64 = 1 << 20
const maxOutputBytes = 256 << 10

type Client struct {
	managedBinary string
	provider      *kubeconfig.SnapshotProvider
	slots         chan struct{}
	endpoint      string
	http          *http.Client
}

func New(endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/mcp" {
		return nil, fmt.Errorf("exec endpoint must be loopback HTTP /mcp")
	}
	// Literal loopback only: no DNS, proxy, caller-selected address or redirect.
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("exec endpoint must use a literal loopback address")
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 5 * time.Second, DisableKeepAlives: true}
	return &Client{endpoint: endpoint, http: &http.Client{Transport: boundedTransport{transport}, Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("upstream redirects disabled") }}}, nil
}

type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Do not propagate the user's JWT/PAT, Cookie or arbitrary identity headers.
	req = req.Clone(req.Context())
	req.Header.Del("Authorization")
	req.Header.Del("Cookie")
	resp, e := t.base.RoundTrip(req)
	if e != nil {
		return nil, e
	}
	resp.Body = &boundedBody{body: resp.Body, remaining: maxResponseBytes}
	return resp, nil
}

type boundedBody struct {
	body      io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		var probe [1]byte
		n, e := b.body.Read(probe[:])
		if n > 0 {
			return 0, fmt.Errorf("upstream response limit exceeded")
		}
		return 0, e
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, e := b.body.Read(p)
	b.remaining -= int64(n)
	return n, e
}
func (b *boundedBody) Close() error { return b.body.Close() }
func (c *Client) Execute(ctx context.Context, ns, pod, container string, argv []string) (domain.PodExecOutput, error) {
	out := domain.PodExecOutput{Outcome: "not_executed"}
	if c.slots != nil {
		select {
		case c.slots <- struct{}{}:
			defer func() { <-c.slots }()
		case <-ctx.Done():
			out.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
			return out, ctx.Err()
		}
	}
	var transport mcp.Transport = &mcp.StreamableClientTransport{Endpoint: c.endpoint, HTTPClient: c.http}
	if c.managedBinary != "" {
		t, cleanup, e := c.managedTransport(ctx)
		if e != nil {
			return out, e
		}
		defer cleanup()
		transport = t
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "k8s-ctl-exec-bridge", Version: "1"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			out.TimedOut = true
			return out, nil
		}
		return out, fmt.Errorf("Kubernetes MCP Server unavailable")
	}
	defer session.Close()
	// This adapter exposes no generic tool proxy or client-controlled tool name.
	out.Outcome = "unknown"
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "pods_exec", Arguments: map[string]any{"namespace": ns, "name": pod, "container": container, "command": argv}})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			out.TimedOut = true
			return out, nil
		}
		return out, fmt.Errorf("upstream exec result unknown; inspect target before retrying")
	}
	if result == nil {
		return out, fmt.Errorf("upstream exec returned no result")
	}
	var text strings.Builder
	for _, item := range result.Content {
		t, ok := item.(*mcp.TextContent)
		if !ok {
			return out, fmt.Errorf("unexpected upstream exec content")
		}
		remaining := maxOutputBytes - text.Len()
		if len(t.Text) > remaining {
			text.WriteString(t.Text[:remaining])
			out.Truncated = true
			break
		}
		text.WriteString(t.Text)
	}
	out.Output = text.String()
	// JSON text is valid UTF-8; byte truncation may split only its final rune.
	for !utf8.ValidString(out.Output) {
		out.Output = out.Output[:len(out.Output)-1]
	}
	out.UpstreamError = result.IsError
	if result.IsError {
		out.Outcome = "upstream_error"
	} else {
		out.Outcome = "completed"
	}
	// The pinned upstream cancels over-limit execution and appends its own notice.
	// Keep the text as-is; its notice is not a machine-readable truncation contract.
	return out, nil
}
