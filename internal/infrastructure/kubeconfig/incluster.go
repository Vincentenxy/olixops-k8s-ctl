package kubeconfig

import (
	"context"
	"fmt"
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// InClusterProvider supplies the same ServiceAccount connection to client-go,
// kubectl and Helm, rather than allowing CLI writes to fall back to ~/.kube/config.
type InClusterProvider struct{}

func (*InClusterProvider) Load(ctx context.Context, _ Reference) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return Document{}, fmt.Errorf("load in-cluster credentials")
	}
	ca := cfg.CAData
	if len(ca) == 0 && cfg.CAFile != "" {
		ca, err = os.ReadFile(cfg.CAFile)
		if err != nil {
			return Document{}, fmt.Errorf("load in-cluster certificate")
		}
	}
	raw := clientcmdapi.NewConfig()
	raw.Clusters["in-cluster"] = &clientcmdapi.Cluster{Server: cfg.Host, CertificateAuthorityData: ca}
	raw.AuthInfos["service-account"] = &clientcmdapi.AuthInfo{Token: cfg.BearerToken, TokenFile: cfg.BearerTokenFile}
	raw.Contexts["in-cluster"] = &clientcmdapi.Context{Cluster: "in-cluster", AuthInfo: "service-account"}
	raw.CurrentContext = "in-cluster"
	data, err := clientcmd.Write(*raw)
	if err != nil {
		return Document{}, fmt.Errorf("encode in-cluster credentials")
	}
	return Document{Bytes: data, Origin: "in-cluster"}, nil
}
