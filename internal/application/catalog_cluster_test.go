package application

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/infrastructure/config"
)

func catalogTestManifest(kinds ...string) string {
	var docs []string
	for idx, kind := range kinds {
		version := "v1"
		if kind == "StatefulSet" || kind == "Deployment" {
			version = "apps/v1"
		}
		docs = append(docs, fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata:\n  name: resource-%d\n  namespace: demo\n", version, kind, idx))
	}
	return strings.Join(docs, "---\n")
}

func TestCatalogClusterValidatesEphemeralAndPersistentWorkloadBoundaries(t *testing.T) {
	bridge := CatalogCluster{Service: NewClusterService(nil, nil, config.KubernetesConfig{AllowedNamespaces: []string{"demo"}})}
	for _, kinds := range [][]string{
		{"Deployment", "Service"},
		{"Deployment", "Service", "ConfigMap"},
		{"StatefulSet", "Service", "PersistentVolumeClaim"},
		{"StatefulSet", "Service", "Service", "PersistentVolumeClaim", "ConfigMap"},
	} {
		require.NoError(t, bridge.Check(context.Background(), catalogTestManifest(kinds...), "demo"))
	}
	for _, kinds := range [][]string{
		{"Deployment", "Service", "PersistentVolumeClaim"},
		{"Deployment", "Service", "Service"},
		{"StatefulSet", "Service"},
		{"Deployment", "StatefulSet", "Service", "PersistentVolumeClaim"},
		{"Deployment", "Service", "Pod"},
	} {
		require.Error(t, bridge.Check(context.Background(), catalogTestManifest(kinds...), "demo"))
	}
	raw := strings.ReplaceAll(catalogTestManifest("Deployment", "Service"), "namespace: demo", "namespace: outside")
	require.Error(t, bridge.Check(context.Background(), raw, "demo"), "allowing Deployments must preserve namespace policy")
}
