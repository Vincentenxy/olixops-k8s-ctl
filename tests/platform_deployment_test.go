package tests

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"olixops-k8s-ctl/internal/infrastructure/config"
)

func TestPlatformDeploymentConfigurations(t *testing.T) {
	for _, profile := range []string{"base", "full"} {
		t.Run(profile, func(t *testing.T) {
			cfg, err := config.Load(filepath.Join("..", "deployments", "platform", profile))
			require.NoError(t, err)
			require.True(t, cfg.HTTPEnabled())
			require.True(t, cfg.Auth.Enabled)
			require.True(t, cfg.Auth.JWT.Enabled)
			require.False(t, cfg.Auth.PAT.Enabled)
			require.False(t, cfg.MCP.Enabled, "platform calls HTTP; private exec sidecar is independent")
			require.False(t, cfg.MCP.DiagnosticsOnly)
			require.True(t, cfg.Kubernetes.InCluster)
			require.True(t, cfg.Kubernetes.WriteEnabled)
			require.Equal(t, "", cfg.Kubernetes.Kubeconfig.Path)
			require.Equal(t, cfg.ClusterID(), cfg.Catalog.Cluster)
			require.Empty(t, cfg.Auth.JWT.Audience, "platform JWT audience is configured from the actual platform contract")
			require.Empty(t, cfg.PodExec.Grants)
			full := profile == "full"
			require.Equal(t, full, cfg.PodExec.Enabled)
			require.Equal(t, full, cfg.Shell.Enabled)
			require.Equal(t, full, cfg.FilesEnabled())
			require.Equal(t, full, cfg.Catalog.Enabled)
			require.Empty(t, cfg.Kubernetes.AllowedNamespaces, "Namespace authorization is managed by platform service permissions")
			require.Empty(t, cfg.PodExec.AllowedNamespaces)
		})
	}
}

// Kustomize is a local renderer: these tests do not contact a cluster or pull images.
func TestPlatformDeploymentContracts(t *testing.T) {
	kubectl, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl is needed for the deployment render contract; config tests still run")
	}
	for _, profile := range []string{"base", "full"} {
		t.Run(profile, func(t *testing.T) {
			root := filepath.Join("..", "deployments", "platform")
			if profile == "full" {
				root = filepath.Join(root, "full")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			raw, err := exec.CommandContext(ctx, kubectl, "kustomize", root).CombinedOutput()
			require.NoError(t, err, "%s", raw)
			objects := map[string]*unstructured.Unstructured{}
			decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
			for {
				obj := &unstructured.Unstructured{}
				err := decoder.Decode(&obj.Object)
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				if len(obj.Object) != 0 {
					key := obj.GetKind() + "/" + obj.GetName()
					require.NotContains(t, objects, key, "duplicate deployment resource")
					objects[key] = obj
				}
			}
			const name = "olixops-k8s-ctl-platform"
			const ns = "olixops-platform"
			deployment := objects["Deployment/"+name]
			require.NotNil(t, deployment)
			require.Equal(t, ns, deployment.GetNamespace(), "isolate legacy app-only Service selectors")
			pod, _, err := unstructured.NestedMap(deployment.Object, "spec", "template", "spec")
			require.NoError(t, err)
			require.Equal(t, name, pod["serviceAccountName"])
			require.Equal(t, ns, objects["ServiceAccount/"+name].GetNamespace())
			volumes := pod["volumes"].([]any)
			secretMounted := false
			for _, v := range volumes {
				volume := v.(map[string]any)
				if ref, ok := volume["configMap"].(map[string]any); ok {
					require.Contains(t, objects, "ConfigMap/"+ref["name"].(string), "generated config reference must resolve")
				}
				if ref, ok := volume["secret"].(map[string]any); ok {
					require.Equal(t, name+"-auth", ref["secretName"])
					secretMounted = true
				}
			}
			require.True(t, secretMounted, "JWT public key must have an explicit Secret mount")
			service := objects["Service/"+name]
			require.NotNil(t, service)
			require.Equal(t, ns, service.GetNamespace())
			selector, _, _ := unstructured.NestedStringMap(service.Object, "spec", "selector")
			labels, _, _ := unstructured.NestedStringMap(deployment.Object, "spec", "template", "metadata", "labels")
			for k, v := range selector {
				require.Equal(t, v, labels[k])
			}
			ports, _, _ := unstructured.NestedSlice(service.Object, "spec", "ports")
			require.Len(t, ports, 1, "private MCP sidecar must not be exposed")
			require.EqualValues(t, 8901, ports[0].(map[string]any)["port"])
			containers := pod["containers"].([]any)
			require.Equal(t, profile == "full", len(containers) == 2)
			_, hasPVC := objects["PersistentVolumeClaim/"+name+"-data"]
			require.Equal(t, profile == "full", hasPVC)
			var loaded *config.Config
			for _, object := range objects {
				require.NotEqual(t, "Secret", object.GetKind(), "do not ship credentials")
				require.NotEqual(t, "Ingress", object.GetKind(), "no automatic external exposure")
				if object.GetKind() == "ConfigMap" {
					data, _, _ := unstructured.NestedStringMap(object.Object, "data")
					if text, ok := data["config.yaml"]; ok {
						dir := t.TempDir()
						require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(text), 0600))
						loaded, err = config.Load(dir)
						require.NoError(t, err)
					}
				}
				if object.GetKind() == "RoleBinding" || object.GetKind() == "ClusterRoleBinding" {
					role, _, _ := unstructured.NestedStringMap(object.Object, "roleRef")
					require.NotEqual(t, "cluster-admin", role["name"])
					require.Contains(t, objects, role["kind"]+"/"+role["name"])
					subjects, _, _ := unstructured.NestedSlice(object.Object, "subjects")
					for _, s := range subjects {
						subject := s.(map[string]any)
						require.Equal(t, name, subject["name"])
						require.Equal(t, ns, subject["namespace"])
					}
				}
			}
			require.NotNil(t, loaded)
			require.Equal(t, profile == "full", loaded.Shell.Enabled, "rendered config replacement must take effect")
			helmRules, _, _ := unstructured.NestedSlice(objects["Role/"+name+"-helm"].Object, "rules")
			require.Contains(t, helmRules[0].(map[string]any)["verbs"], "create", "Helm releases need Secret writes")
			nsRules, _, _ := unstructured.NestedSlice(objects["ClusterRole/"+name+"-namespace-metadata"].Object, "rules")
			require.Equal(t, []any{"developer-demo"}, nsRules[0].(map[string]any)["resourceNames"], "namespace deletion must remain scoped")
		})
	}
}
