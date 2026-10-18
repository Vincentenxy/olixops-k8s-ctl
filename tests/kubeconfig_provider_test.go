package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
)

func TestFileKubeconfigProvider(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config")
	content := []byte("apiVersion: v1\nkind: Config\n")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	provider := kubeconfig.NewFileProvider()
	document, err := provider.Load(context.Background(), kubeconfig.Reference{Source: kubeconfig.SourceFile, Path: path})
	require.NoError(t, err)
	require.Equal(t, content, document.Bytes)
	require.Equal(t, path, document.Origin)
}

type fakeDatabaseReader struct {
	data []byte
}

func (f fakeDatabaseReader) ReadKubeconfig(_ context.Context, reference string) ([]byte, error) {
	if reference != "cluster-1" {
		return nil, os.ErrNotExist
	}
	return f.data, nil
}

func TestDatabaseKubeconfigProvider(t *testing.T) {
	provider, err := kubeconfig.NewProvider(kubeconfig.Reference{Source: kubeconfig.SourceDatabase, Reference: "cluster-1"}, fakeDatabaseReader{data: []byte("database-config")})
	require.NoError(t, err)

	document, err := provider.Load(context.Background(), kubeconfig.Reference{Source: kubeconfig.SourceDatabase, Reference: "cluster-1"})
	require.NoError(t, err)
	require.Equal(t, []byte("database-config"), document.Bytes)
	require.Equal(t, "database:cluster-1", document.Origin)
}

func TestFileKubeconfigRelativeReferencesSurviveTemporaryCopy(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config")
	data := []byte("apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: https://example.invalid\n    certificate-authority: ca.crt\nusers:\n- name: test\n  user:\n    client-certificate: cert.pem\n    client-key: key.pem\n    tokenFile: token\n")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	document, err := kubeconfig.NewFileProvider().Load(context.Background(), kubeconfig.Reference{Path: path})
	require.NoError(t, err)
	for _, name := range []string{"ca.crt", "cert.pem", "key.pem", "token"} {
		require.Contains(t, string(document.Bytes), filepath.Join(directory, name))
	}
}
