package kubeconfig

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotPinsSelectedContextAndTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("fixture-token-one"), 0600))
	file := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(file, []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: first
contexts:
- name: first
  context: {cluster: first, user: one}
- name: second
  context: {cluster: second, user: two}
clusters:
- name: first
  cluster: {server: "https://first.invalid"}
- name: second
  cluster: {server: "https://second.invalid"}
users:
- name: one
  user: {token: fixture-unused}
- name: two
  user: {tokenFile: %q}
`, tokenFile)), 0600))
	snapshot, err := Snapshot(context.Background(), NewFileProvider(), Reference{Path: file, Context: "second"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tokenFile, []byte("fixture-token-changed"), 0600))
	document, err := snapshot.Load(context.Background(), Reference{})
	require.NoError(t, err)
	cfg, err := clientcmd.Load(document.Bytes)
	require.NoError(t, err)
	require.Len(t, cfg.Clusters, 1)
	require.Len(t, cfg.Contexts, 1)
	require.Len(t, cfg.AuthInfos, 1)
	require.Equal(t, "second", cfg.CurrentContext)
	require.Equal(t, "fixture-token-one", cfg.AuthInfos["two"].Token)
	require.Empty(t, cfg.AuthInfos["two"].TokenFile)
	document.Bytes[0] = 'x'
	again, err := snapshot.Load(context.Background(), Reference{})
	require.NoError(t, err)
	require.NotEqual(t, byte('x'), again.Bytes[0])
}
