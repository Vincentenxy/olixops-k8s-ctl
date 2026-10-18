package kubeconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
)

const (
	SourceInCluster = "in-cluster"
	SourceFile      = "file"
	SourceDatabase  = "database"
)

type Reference struct {
	Source    string
	Path      string
	Reference string
	Context   string
}

type Document struct {
	Bytes  []byte
	Origin string
}

// Provider loads kubeconfig bytes without exposing their contents to callers.
// Implementations may read from a file, database, secret manager, or another
// controlled source.
type Provider interface {
	Load(ctx context.Context, reference Reference) (Document, error)
}

type FileProvider struct {
	HomeDir func() (string, error)
}

func NewFileProvider() *FileProvider {
	return &FileProvider{HomeDir: os.UserHomeDir}
}

func (p *FileProvider) Load(_ context.Context, reference Reference) (Document, error) {
	path, err := p.resolvePath(reference.Path)
	if err != nil {
		return Document{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("read kubeconfig file: %w", err)
	}
	// Resolve references here, while the original file location is still known.
	// CLI adapters receive a temporary document and must not know its source path.
	config, err := clientcmd.Load(data)
	if err != nil {
		return Document{}, fmt.Errorf("parse kubeconfig file")
	}
	hasReferences := false
	for _, cluster := range config.Clusters {
		cluster.LocationOfOrigin = path
		hasReferences = hasReferences || cluster.CertificateAuthority != ""
	}
	for _, user := range config.AuthInfos {
		user.LocationOfOrigin = path
		hasReferences = hasReferences || user.ClientCertificate != "" || user.ClientKey != "" || user.TokenFile != ""
	}
	if hasReferences {
		if err := clientcmd.ResolveLocalPaths(config); err != nil {
			return Document{}, fmt.Errorf("resolve kubeconfig file references")
		}
		data, err = clientcmd.Write(*config)
		if err != nil {
			return Document{}, fmt.Errorf("encode kubeconfig file references")
		}
	}
	return Document{Bytes: data, Origin: path}, nil
}

func (p *FileProvider) resolvePath(path string) (string, error) {
	path = os.ExpandEnv(strings.TrimSpace(path))
	if path == "" {
		home, err := p.HomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for kubeconfig: %w", err)
		}
		path = filepath.Join(home, ".kube", "config")
	} else if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := p.HomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for kubeconfig: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve kubeconfig path: %w", err)
	}
	return filepath.Clean(absolute), nil
}

type DatabaseReader interface {
	ReadKubeconfig(ctx context.Context, reference string) ([]byte, error)
}

type DatabaseProvider struct {
	Reader DatabaseReader
}

func (p *DatabaseProvider) Load(ctx context.Context, reference Reference) (Document, error) {
	if p == nil || p.Reader == nil {
		return Document{}, fmt.Errorf("database kubeconfig provider is not configured")
	}
	if strings.TrimSpace(reference.Reference) == "" {
		return Document{}, fmt.Errorf("database kubeconfig reference is required")
	}
	data, err := p.Reader.ReadKubeconfig(ctx, reference.Reference)
	if err != nil {
		return Document{}, fmt.Errorf("read kubeconfig from database: %w", err)
	}
	return Document{Bytes: data, Origin: "database:" + reference.Reference}, nil
}

func NewProvider(reference Reference, reader DatabaseReader) (Provider, error) {
	source := strings.ToLower(strings.TrimSpace(reference.Source))
	if source == "" {
		source = SourceFile
	}
	switch source {
	case SourceInCluster:
		return &InClusterProvider{}, nil
	case SourceFile:
		return NewFileProvider(), nil
	case SourceDatabase:
		return &DatabaseProvider{Reader: reader}, nil
	default:
		return nil, fmt.Errorf("unsupported kubeconfig source %q", source)
	}
}
