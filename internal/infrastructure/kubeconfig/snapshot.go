package kubeconfig

import (
	"context"
	"fmt"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"os"
	"strings"
)

// SnapshotProvider pins the same selected cluster for preflight and managed MCP execution.
// Contents are private and only serialized into a temporary server-side credential file.
type SnapshotProvider struct{ data []byte }

func Snapshot(ctx context.Context, provider Provider, ref Reference) (*SnapshotProvider, error) {
	document, err := provider.Load(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("load cluster snapshot failed")
	}
	cfg, err := clientcmd.Load(document.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse cluster snapshot failed")
	}
	if ref.Context != "" {
		cfg.CurrentContext = ref.Context
	}
	if err = clientcmdapi.MinifyConfig(cfg); err != nil {
		return nil, fmt.Errorf("select cluster snapshot failed")
	}
	if err = clientcmdapi.FlattenConfig(cfg); err != nil {
		return nil, fmt.Errorf("resolve cluster snapshot failed")
	}
	for _, auth := range cfg.AuthInfos {
		if auth.TokenFile != "" {
			token, e := os.ReadFile(auth.TokenFile)
			if e != nil {
				return nil, fmt.Errorf("resolve cluster token file failed")
			}
			auth.Token = strings.TrimSpace(string(token))
			if auth.Token == "" {
				return nil, fmt.Errorf("cluster token file is empty")
			}
			auth.TokenFile = ""
		}
	}
	data, err := clientcmd.Write(*cfg)
	if err != nil {
		return nil, fmt.Errorf("encode cluster snapshot failed")
	}
	return &SnapshotProvider{data: data}, nil
}
func (s *SnapshotProvider) Load(ctx context.Context, _ Reference) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	return Document{Bytes: append([]byte{}, s.data...), Origin: "selected-cluster-snapshot"}, nil
}
