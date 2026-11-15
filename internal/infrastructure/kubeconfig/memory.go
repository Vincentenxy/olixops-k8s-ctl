package kubeconfig

import (
	"context"
	"fmt"
	"k8s.io/client-go/tools/clientcmd"
	"net/url"
)

// MemoryProvider accepts only self-contained documents supplied by a trusted platform.
// It never resolves paths or executes kubeconfig credential plugins on this host.
type MemoryProvider struct{ data []byte }

func NewMemoryProvider(data []byte) (*MemoryProvider, error) {
	if len(data) == 0 || len(data) > 128*1024 {
		return nil, fmt.Errorf("invalid kubeconfig size")
	}
	cfg, err := clientcmd.Load(data)
	if err != nil || cfg.CurrentContext == "" {
		return nil, fmt.Errorf("invalid kubeconfig document")
	}
	for _, c := range cfg.Clusters {
		u, e := url.Parse(c.Server)
		if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || c.CertificateAuthority != "" || c.ProxyURL != "" {
			return nil, fmt.Errorf("cluster must use an embedded certificate and a direct HTTP(S) endpoint")
		}
	}
	for _, a := range cfg.AuthInfos {
		if a.Exec != nil || a.AuthProvider != nil || a.TokenFile != "" || a.ClientCertificate != "" || a.ClientKey != "" || a.Impersonate != "" || len(a.ImpersonateGroups) > 0 || len(a.ImpersonateUserExtra) > 0 {
			return nil, fmt.Errorf("external credentials and impersonation are not supported")
		}
	}
	if err = clientcmd.Validate(*cfg); err != nil {
		return nil, fmt.Errorf("kubeconfig validation failed")
	}
	return &MemoryProvider{data: append([]byte(nil), data...)}, nil
}
func (p *MemoryProvider) Load(ctx context.Context, _ Reference) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	return Document{Bytes: append([]byte(nil), p.data...), Origin: "platform-memory"}, nil
}
