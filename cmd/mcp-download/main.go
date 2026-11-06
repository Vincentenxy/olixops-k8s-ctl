// mcp-download prepares through MCP and streams HTTP to a local file; no kubeconfig.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"olixops-k8s-ctl/internal/domain"
)

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(copy)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Download failed; no completed file saved. Check PAT, target, tar, destination and network.")
		os.Exit(1)
	}
}
func run() error {
	endpoint := flag.String("url", "http://127.0.0.1:8091/api/v1/mcp", "authenticated MCP URL")
	tokenEnv := flag.String("token-env", "K8S_CTL_PAT", "PAT environment variable")
	ns := flag.String("namespace", "", "Namespace")
	pod := flag.String("pod", "", "Pod")
	container := flag.String("container", "", "container")
	remote := flag.String("path", "", "absolute container file")
	dest := flag.String("out", "", "new local file (never overwritten)")
	flag.Parse()
	u, err := url.Parse(*endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" && (u.Scheme != "http" || u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return fmt.Errorf("invalid URL")
	}
	token := os.Getenv(*tokenEnv)
	if token == "" || *dest == "" || *ns == "" || *pod == "" || *container == "" || *remote == "" {
		return fmt.Errorf("missing input")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client := &http.Client{Transport: bearer{token, http.DefaultTransport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	prepareCtx, prepareCancel := context.WithTimeout(ctx, 35*time.Second)
	session, err := sdk.NewClient(&sdk.Implementation{Name: "k8s-ctl-file-download", Version: "2"}, nil).Connect(prepareCtx, &sdk.StreamableClientTransport{Endpoint: *endpoint, HTTPClient: client}, nil)
	if err != nil {
		prepareCancel()
		return err
	}
	result, err := session.CallTool(prepareCtx, &sdk.CallToolParams{Name: "download_pod_file", Arguments: domain.DownloadInput{TerminalTarget: domain.TerminalTarget{Namespace: *ns, Pod: *pod, Container: *container}, Path: *remote}})
	session.Close()
	prepareCancel()
	if err != nil {
		return err
	}
	if result.IsError {
		return fmt.Errorf("prepare failed")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	var ticket domain.DownloadOutput
	if err = json.Unmarshal(raw, &ticket); err != nil {
		return err
	}
	target, err := url.Parse(ticket.DownloadURL)
	if err != nil {
		return err
	}
	if !target.IsAbs() {
		target.Scheme = u.Scheme
		target.Host = u.Host
		prefix := strings.TrimSuffix(u.Path, "/api/v1/mcp")
		target.Path = prefix + target.Path
	}
	// Never forward a PAT to a different origin supplied in tool output.
	if target.Scheme != u.Scheme || target.Host != u.Host || target.User != nil || target.RawQuery != "" || target.Fragment != "" || ticket.DownloadID == "" || ticket.Method != "POST" {
		return fmt.Errorf("invalid download reference")
	}
	body, _ := json.Marshal(domain.DownloadRequest{DownloadID: ticket.DownloadID})
	req, err := http.NewRequestWithContext(ctx, "POST", target.String(), strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/octet-stream") {
		return fmt.Errorf("download rejected")
	}
	output, err := os.CreateTemp(filepath.Dir(*dest), ".k8s-download-*.part")
	if err != nil {
		return err
	}
	tmp := output.Name()
	defer os.Remove(tmp)
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, hash), response.Body)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(tmp, *dest); err != nil {
		return err
	}
	fmt.Printf("Saved %d bytes to %s; local SHA256 %x\n", n, *dest, hash.Sum(nil))
	return nil
}
