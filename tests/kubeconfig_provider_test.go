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
