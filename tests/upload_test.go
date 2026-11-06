package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"olixops-k8s-ctl/internal/domain"
	authdomain "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/internal/infrastructure/config"
	k8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/router"
)

type uploadBackend struct {
	sync.Mutex
	stages      map[string][]byte
	files       map[string][]byte
	failReceive bool
	failStage   bool
	corrupt     bool
}

func (b *uploadBackend) Stream(ctx context.Context, _, _, _ string, argv []string, tty bool, in io.Reader, out, stderr io.Writer) error {
	if tty || len(argv) < 5 || argv[0] != "/bin/sh" || argv[1] != "-c" || argv[3] != "k8s-ctl-upload" {
		return fmt.Errorf("unexpected stream command")
	}
	stage := argv[4]
	if strings.Contains(argv[2], "mkdir") {
		b.Lock()
		if b.failStage {
			b.Unlock()
			return fmt.Errorf("read-only filesystem")
		}
		b.stages[stage] = nil
		b.Unlock()
		return nil
	}
	if strings.Contains(argv[2], "head -c") {
		size, e := strconv.ParseInt(argv[6], 10, 64)
		if e != nil {
			return e
		}
		data := make([]byte, int(size))
		_, err := io.ReadFull(in, data)
		b.Lock()
		b.stages[stage] = data
		fail := b.failReceive
		corrupt := b.corrupt
		b.Unlock()
		if fail {
			return fmt.Errorf("receive failed after data")
		}
		if corrupt {
			fmt.Fprint(out, "invalid-digest  -\n")
		} else {
			fmt.Fprintf(out, "%x  -\n", sha256.Sum256(data))
		}
		return err
	}
	b.Lock()
	defer b.Unlock()
	if strings.HasPrefix(argv[2], "ln ") {
		if len(argv) != 7 {
			return fmt.Errorf("missing path argv")
		}
		destination := path.Join(argv[5], argv[6])
		if _, ok := b.files[destination]; ok {
			return fmt.Errorf("destination exists")
		}
		b.files[destination] = append([]byte{}, b.stages[stage]...)
		return nil
	}
	if strings.HasPrefix(argv[2], "rm -f ") {
		delete(b.stages, stage)
		return nil
	}
	return fmt.Errorf("unexpected script")
}

// Only this test adapter executes the server-owned scripts on a temporary local
// filesystem. Production delegates all such commands to Kubernetes SPDY exec.
type localUploadBackend struct{ root string }

func (b localUploadBackend) Stream(ctx context.Context, _, _, _ string, argv []string, tty bool, in io.Reader, out, stderr io.Writer) error {
	if len(argv) < 5 || !strings.HasPrefix(filepath.Clean(argv[4]), b.root+string(os.PathSeparator)) {
		return fmt.Errorf("outside temporary test directory")
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if in != nil {
		stdin, e := command.StdinPipe()
		if e != nil {
			return e
		}
		defer stdin.Close()
		go func() { _, _ = io.Copy(stdin, in); _ = stdin.Close() }()
	}
	command.Stdout = out
	command.Stderr = stderr
	return command.Run()
}

type brokenUpload struct{ sent bool }

func (b *brokenUpload) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, "partial"), nil
	}
	return 0, io.ErrUnexpectedEOF
}
func (*brokenUpload) Close() error { return nil }

func TestUploadThroughMainMCP(t *testing.T) {
	f := newAuthFixture(t)
	var uid atomic.Value
	uid.Store("uid-1")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"app","namespace":"demo","uid":%q,"labels":{"k8s-ctl/exec-enabled":"true"}},"spec":{"containers":[{"name":"app"}]},"status":{"containerStatuses":[{"name":"app","state":{"running":{}}}]}}`, uid.Load())
	}))
	defer api.Close()
	provider := memoryKubeconfig(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: %s\ncontexts:\n- name: test\n  context:\n    cluster: test\n    user: test\ncurrent-context: test\nusers:\n- name: test\n  user: {}\n", api.URL))
	f.cfg.Kubernetes.InCluster = true
	f.cfg.Kubernetes.WriteEnabled = true
	f.cfg.Kubernetes.AllowedNamespaces = []string{"demo"}
	f.cfg.MCP.WriteEnabled = true
	f.cfg.Shell.Enabled = true
	f.cfg.PodExec = config.PodExecConfig{Enabled: true, AllowedNamespaces: []string{"demo"}}
	client, err := k8s.New(f.cfg.Kubernetes, provider)
	require.NoError(t, err)
	manager := sessions.NewManager(time.Minute, time.Hour, 4, 2)
	defer manager.Close()
	backend := &uploadBackend{stages: map[string][]byte{}, files: map[string][]byte{}}
	service := application.NewClusterService(client, nil, f.cfg.Kubernetes, application.WithPodExecutor(&countingExecutor{}, []string{"demo"}), application.WithShell(true, true, interceptor.AllowAll{}), application.WithTerminals(backend, manager))
	engine, err := router.NewWithAuth(f.cfg, service, nil, f.service)
	require.NoError(t, err)
	server := httptest.NewServer(engine)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	token := f.company(t, nil, nil)
	session, err := sdk.NewClient(&sdk.Implementation{Name: "upload-test", Version: "1"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token}}}, nil)
	require.NoError(t, err)
	defer session.Close()
	target := domain.TerminalTarget{Namespace: "demo", Pod: "app", Container: "app"}
	prepare := func(path string) domain.UploadOutput {
		t.Helper()
		result, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "upload_pod_file", Arguments: domain.UploadInput{TerminalTarget: target, Path: path}})
		require.NoError(t, e)
		require.False(t, result.IsError)
		raw, e := json.Marshal(result.StructuredContent)
		require.NoError(t, e)
		var ticket domain.UploadOutput
		require.NoError(t, json.Unmarshal(raw, &ticket))
		require.Equal(t, "POST", ticket.Method)
		return ticket
	}
	send := func(ticket domain.UploadOutput, token string, data []byte, contentRange string) int {
		t.Helper()
		req, e := http.NewRequest("POST", server.URL+application.UploadRoute, bytes.NewReader(data))
		require.NoError(t, e)
		req.Header.Set("Content-Type", "application/octet-stream")
		if len(data) > 1<<20 {
			req.Header.Set("Expect", "100-continue")
		}
		req.Header.Set("X-Upload-Id", ticket.UploadID)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Range", contentRange)
		resp, e := http.DefaultClient.Do(req)
		require.NoError(t, e)
		defer resp.Body.Close()
		raw, e := io.ReadAll(resp.Body)
		require.NoError(t, e)
		if resp.StatusCode == 200 {
			var envelope struct {
				Code int                 `json:"code"`
				Data domain.UploadResult `json:"data"`
			}
			require.NoError(t, json.Unmarshal(raw, &envelope))
			require.Zero(t, envelope.Code)
			require.Equal(t, int64(len(data)), envelope.Data.Size)
			require.Equal(t, "uid-1", envelope.Data.PodUID)
		}
		return resp.StatusCode
	}
	data := bytes.Repeat([]byte{0, 255, 128, 1, 10}, (1<<20)/5+1)
	ticket := prepare("/tmp/data.bin")
	// Unknown-length/chunked uploads are rejected before consuming the reference.
	unknown, e := http.NewRequest("POST", server.URL+application.UploadRoute, io.NopCloser(bytes.NewReader(data)))
	require.NoError(t, e)
	unknown.ContentLength = -1
	unknown.Header.Set("Authorization", "Bearer "+token)
	unknown.Header.Set("Content-Type", "application/octet-stream")
	unknown.Header.Set("X-Upload-Id", ticket.UploadID)
	unknownResponse, e := http.DefaultClient.Do(unknown)
	require.NoError(t, e)
	unknownResponse.Body.Close()
	require.Equal(t, 400, unknownResponse.StatusCode)

	require.Equal(t, 401, send(ticket, "", data, ""))
	require.Equal(t, 404, send(ticket, f.company(t, func(c jwt.MapClaims) { c["staffuserid"] = "other" }, nil), data, ""))
	require.Equal(t, 400, send(ticket, token, data, "bytes 0-1/2"))
	require.Equal(t, 200, send(ticket, token, data, ""))
	require.Equal(t, 404, send(ticket, token, data, ""))
	backend.Lock()
	require.Equal(t, data, backend.files["/tmp/data.bin"])
	require.Empty(t, backend.stages)
	backend.Unlock()
	// Reject an unwritable parent before reading a large/Expect-continue body.
	backend.Lock()
	backend.failStage = true
	backend.Unlock()
	started := time.Now()
	require.Equal(t, 503, send(prepare("/tmp/read-only.bin"), token, data, ""))
	require.Less(t, time.Since(started), 5*time.Second)
	backend.Lock()
	backend.failStage = false
	backend.Unlock()
	// Existing destination must retain its bytes.
	require.Equal(t, 502, send(prepare("/tmp/data.bin"), token, []byte("replace"), ""))
	backend.Lock()
	require.Equal(t, data, backend.files["/tmp/data.bin"])
	backend.Unlock()
	require.Equal(t, 200, send(prepare("/tmp/empty.bin"), token, nil, ""))
	backend.Lock()
	backend.failReceive = true
	backend.Unlock()
	require.Equal(t, 503, send(prepare("/tmp/failed.bin"), token, data, ""))
	backend.Lock()
	_, exists := backend.files["/tmp/failed.bin"]
	require.False(t, exists)
	require.Empty(t, backend.stages)
	backend.failReceive = false
	backend.Unlock()
	alice := authdomain.WithPrincipal(ctx, authdomain.Principal{UserID: "wx-user"})
	incomplete := prepare("/tmp/incomplete.bin")
	_, err = service.ReceivePodFile(alice, incomplete.UploadID, &brokenUpload{}, 100)
	require.Error(t, err)
	backend.Lock()
	_, exists = backend.files["/tmp/incomplete.bin"]
	require.False(t, exists)
	require.Empty(t, backend.stages)
	backend.Unlock()
	// A remote success with the wrong digest must never publish a file.
	backend.Lock()
	backend.corrupt = true
	backend.Unlock()
	require.Equal(t, 503, send(prepare("/tmp/corrupt.bin"), token, data, ""))
	backend.Lock()
	_, exists = backend.files["/tmp/corrupt.bin"]
	require.False(t, exists)
	require.Empty(t, backend.stages)
	backend.corrupt = false
	backend.Unlock()
	// Policy is checked again on actual upload, not only during preparation.
	denied := prepare("/tmp/denied.bin")
	application.WithShell(true, true, &shellTestPolicy{deny: true})(service)
	require.Equal(t, 403, send(denied, token, data, ""))
	application.WithShell(true, true, interceptor.AllowAll{})(service)
	replaced := prepare("/tmp/replaced.bin")
	uid.Store("uid-2")
	require.Equal(t, 409, send(replaced, token, data, ""))
	_, err = service.UploadPodFile(alice, domain.UploadInput{TerminalTarget: target, Path: "/proc/inject"})
	require.Error(t, err)
	// Exercise the actual fixed scripts, including directory/symlink collisions.
	uid.Store("uid-1")
	directory := t.TempDir()
	application.WithTerminals(localUploadBackend{root: directory}, manager)(service)
	localPath := filepath.Join(directory, "new.bin")
	require.Equal(t, 200, send(prepare(localPath), token, data, ""))
	actual, e := os.ReadFile(localPath)
	require.NoError(t, e)
	require.Equal(t, data, actual)
	stat, e := os.Stat(localPath)
	require.NoError(t, e)
	require.Equal(t, os.FileMode(0600), stat.Mode().Perm())
	require.Equal(t, 502, send(prepare(localPath), token, []byte("overwrite"), ""))
	existingDir := filepath.Join(directory, "existing-dir")
	require.NoError(t, os.Mkdir(existingDir, 0700))
	require.Equal(t, 502, send(prepare(existingDir), token, data, ""))
	entries, e := os.ReadDir(existingDir)
	require.NoError(t, e)
	require.Empty(t, entries)
	symlink := filepath.Join(directory, "linked.bin")
	require.NoError(t, os.Symlink(localPath, symlink))
	require.Equal(t, 502, send(prepare(symlink), token, data, ""))
	actual, e = os.ReadFile(localPath)
	require.NoError(t, e)
	require.Equal(t, data, actual)
	entries, e = os.ReadDir(directory)
	require.NoError(t, e)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".k8s-ctl-upload-"))
	}
	uid.Store("uid-2")
	// A download grant does not implicitly grant writes.
	application.WithDiagnosticGrants([]domain.DiagnosticGrant{{UserID: "wx-user", Namespace: "demo", Pod: "app", Container: "app", PodUID: "uid-2", Actions: []string{"download_file"}}})(service)
	_, err = service.UploadPodFile(alice, domain.UploadInput{TerminalTarget: target, Path: "/tmp/no-grant.bin"})
	require.Error(t, err)
}
