package catalog

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

func templateDocuments(t *testing.T, raw string) []*unstructured.Unstructured {
	t.Helper()
	dec := kyaml.NewYAMLOrJSONDecoder(strings.NewReader(raw), 4096)
	var out []*unstructured.Unstructured
	for {
		var obj map[string]any
		err := dec.Decode(&obj)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if len(obj) > 0 {
			out = append(out, &unstructured.Unstructured{Object: obj})
		}
	}
	return out
}
func renderCurrent(t *testing.T, sw string, over d.Parameters) (string, []*unstructured.Unstructured) {
	t.Helper()
	seed := seedsFor(sw)
	p := merge(seed.Defaults, over)
	if p.StorageClass == "" {
		p.StorageClass = "standard"
	}
	require.NoError(t, validateParams(sw, p))
	raw, err := render(seed, d.Instance{ID: "demo-instance", ResourceName: "demo", Namespace: "development", Software: sw}, p, 3)
	require.NoError(t, err)
	return raw, templateDocuments(t, raw)
}
func workloadContainer(t *testing.T, docs []*unstructured.Unstructured) map[string]any {
	t.Helper()
	for _, obj := range docs {
		if obj.GetKind() != "Deployment" && obj.GetKind() != "StatefulSet" {
			continue
		}
		containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
		require.NoError(t, err)
		require.True(t, found)
		require.Len(t, containers, 1)
		return containers[0].(map[string]any)
	}
	t.Fatal("missing workload")
	return nil
}
func TestCurrentTemplatesRenderAllSevenProfiles(t *testing.T) {
	require.Len(t, d.Profiles(), 7)
	for _, profile := range d.Profiles() {
		t.Run(profile.Software, func(t *testing.T) {
			raw, docs := renderCurrent(t, profile.Software, d.Parameters{})
			require.NotContains(t, raw, "kind: Secret")
			require.NotContains(t, raw, "secretKeyRef:")
			count := map[string]int{}
			for _, obj := range docs {
				count[obj.GetKind()]++
				require.Equal(t, "development", obj.GetNamespace())
				require.Equal(t, "demo-instance", obj.GetLabels()[d.InstanceLabel])
				if obj.GetKind() == profile.WorkloadKind {
					value, found, err := unstructured.NestedBool(obj.Object, "spec", "template", "spec", "automountServiceAccountToken")
					require.NoError(t, err)
					require.True(t, found)
					require.False(t, value)
					require.Equal(t, "3", obj.GetAnnotations()[d.RevisionAnnotation])
				}
			}
			require.Equal(t, 1, count[profile.WorkloadKind])
			require.Equal(t, 1, count["ConfigMap"])
			if profile.Persistent {
				require.Equal(t, 1, count["PersistentVolumeClaim"])
				require.Equal(t, 2, count["Service"])
			} else {
				require.Zero(t, count["PersistentVolumeClaim"])
				require.Equal(t, 1, count["Service"])
			}
			ports, found, err := unstructured.NestedSlice(workloadContainer(t, docs), "ports")
			require.NoError(t, err)
			require.True(t, found)
			require.EqualValues(t, profile.BackendPort, ports[0].(map[string]any)["containerPort"])
		})
	}
}
func TestCurrentTemplatesAuthenticationModes(t *testing.T) {
	for _, sw := range []string{"redis", "postgres", "mysql", "mongodb"} {
		t.Run(sw, func(t *testing.T) {
			raw, _ := renderCurrent(t, sw, d.Parameters{})
			switch sw {
			case "redis":
				require.Contains(t, raw, "protected-mode no")
				require.NotContains(t, raw, "requirepass")
			case "postgres":
				require.Contains(t, raw, "POSTGRES_HOST_AUTH_METHOD")
				require.Contains(t, raw, "trust")
			case "mysql":
				require.Contains(t, raw, "MYSQL_ALLOW_EMPTY_PASSWORD")
			case "mongodb":
				require.NotContains(t, raw, "MONGO_INITDB_ROOT_PASSWORD")
			}
			raw, _ = renderCurrent(t, sw, d.Parameters{SecretName: "dev-credentials"})
			require.Contains(t, raw, "secretKeyRef:")
			require.Contains(t, raw, "dev-credentials")
			require.NotContains(t, raw, "kind: Secret")
			switch sw {
			case "redis":
				require.Contains(t, raw, "requirepass")
				require.Contains(t, raw, "protected-mode yes")
			case "postgres":
				require.NotContains(t, raw, "POSTGRES_HOST_AUTH_METHOD")
			case "mysql":
				require.Contains(t, raw, "MYSQL_ROOT_PASSWORD")
				require.NotContains(t, raw, "MYSQL_ALLOW_EMPTY_PASSWORD")
			case "mongodb":
				require.Contains(t, raw, "MONGO_INITDB_ROOT_USERNAME")
				require.Contains(t, raw, "MONGO_INITDB_ROOT_PASSWORD")
			}
		})
	}
	raw, _ := renderCurrent(t, "mysql", d.Parameters{SecretName: "mysql-password", Username: "appuser"})
	require.Contains(t, raw, "MYSQL_USER")
	require.Contains(t, raw, "MYSQL_PASSWORD")
	require.Contains(t, raw, "appuser")
}
func TestMilvusEmbeddedStorageAndSandboxIsolation(t *testing.T) {
	raw, docs := renderCurrent(t, "milvus", d.Parameters{})
	for _, want := range []string{"ETCD_USE_EMBED", "ETCD_CONFIG_PATH", "127.0.0.1:2379", "COMMON_STORAGETYPE", "rocksmq", "/milvus/configs/user.yaml", "/var/lib/milvus", "9091", "Unconfined"} {
		require.Contains(t, raw, want)
	}
	require.NotContains(t, raw, "minio")
	container := workloadContainer(t, docs)
	command, _, err := unstructured.NestedStringSlice(container, "command")
	require.NoError(t, err)
	require.Equal(t, []string{"milvus"}, command)
	raw, docs = renderCurrent(t, "sandbox", d.Parameters{RuntimeClassName: "gvisor"})
	for _, want := range []string{"gvisor", "runAsNonRoot: true", "runAsUser: 1000", "allowPrivilegeEscalation: false", "RuntimeDefault", "emptyDir:", "IdentityProvider.token=", "allow_remote_access=True"} {
		require.Contains(t, raw, want)
	}
	require.NotContains(t, raw, "hostPath")
	require.NotContains(t, raw, "persistentVolumeClaim")
	args, _, err := unstructured.NestedStringSlice(workloadContainer(t, docs), "args")
	require.NoError(t, err)
	require.Equal(t, "start-notebook.py", args[0])
}
func TestNginxContentIsDataAndSchemasUseCorrectTypes(t *testing.T) {
	content := "<h1>custom</h1>\n---\nkind: Secret\n"
	_, docs := renderCurrent(t, "nginx", d.Parameters{Content: content})
	require.Len(t, docs, 3)
	for _, obj := range docs {
		if obj.GetKind() == "ConfigMap" {
			got, _, err := unstructured.NestedString(obj.Object, "data", "index.html")
			require.NoError(t, err)
			require.Equal(t, content, got)
		}
	}
	for _, profile := range d.Profiles() {
		var schema map[string]any
		require.NoError(t, json.Unmarshal([]byte(seedsFor(profile.Software).ParameterSchema), &schema))
		properties := schema["properties"].(map[string]any)
		if profile.Software == "sandbox" {
			require.Equal(t, "integer", properties["ttlSeconds"].(map[string]any)["type"])
		}
		required := schema["required"].([]any)
		require.NotContains(t, required, "secretName")
		if !profile.Persistent {
			require.NotContains(t, required, "storage")
			require.NotContains(t, required, "storageClass")
		}
	}
}
func TestTemplateValidationRejectsUnsupportedInputs(t *testing.T) {
	for _, tt := range []struct {
		name, sw string
		over     d.Parameters
	}{
		{"bad quantity", "redis", d.Parameters{CPU: "0"}},
		{"bad storage class", "mysql", d.Parameters{StorageClass: "bad/class"}},
		{"bad secret", "mongodb", d.Parameters{SecretName: "bad/secret"}},
		{"secret not supported", "milvus", d.Parameters{SecretName: "credentials"}},
		{"invalid database", "postgres", d.Parameters{Database: "app;drop"}},
		{"mysql noauth app user", "mysql", d.Parameters{Username: "appuser"}},
		{"runtime on database", "redis", d.Parameters{RuntimeClassName: "gvisor"}},
		{"content on database", "mongodb", d.Parameters{Content: "page"}},
		{"sandbox no expiry", "sandbox", d.Parameters{TTLSeconds: -1}},
		{"sandbox excessive ttl", "sandbox", d.Parameters{TTLSeconds: 86401}},
		{"sandbox bad runtime", "sandbox", d.Parameters{RuntimeClassName: "../gvisor"}},
		{"nginx oversized content", "nginx", d.Parameters{Content: strings.Repeat("x", (256<<10)+1)}},
		{"image registry port is not tag", "redis", d.Parameters{Image: "registry.local:5000/redis"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := merge(seedsFor(tt.sw).Defaults, tt.over)
			if p.StorageClass == "" {
				p.StorageClass = "standard"
			}
			require.Error(t, validateParams(tt.sw, p))
		})
	}
	for _, sw := range []string{"nginx", "sandbox"} {
		require.NoError(t, validateParams(sw, seedsFor(sw).Defaults))
	}
}
func TestLegacyTemplatesAndTemplateResourceBoundary(t *testing.T) {
	require.Len(t, seeds(), 9)
	for _, seed := range seeds() {
		if seed.Version != 1 || (seed.Software != "redis" && seed.Software != "postgres") {
			continue
		}
		require.Equal(t, baseTemplate, seed.ManifestTemplate)
		p := merge(seed.Defaults, d.Parameters{StorageClass: "standard", SecretName: "legacy-password"})
		instance := d.Instance{ID: "legacy", ResourceName: "legacy", Namespace: "development", Software: seed.Software}
		_, err := render(seed, instance, p, 1)
		require.NoError(t, err)
		p.SecretName = ""
		_, err = render(seed, instance, p, 1)
		require.Error(t, err)
	}
	for _, manifest := range []string{
		"apiVersion: v1\nkind: Secret\nmetadata: {name: bad, namespace: development}\n",
		"apiVersion: v1\nkind: Service\nmetadata: {name: bad, namespace: other}\n",
		"apiVersion: gateway.networking.k8s.io/v1alpha2\nkind: TCPRoute\nmetadata: {name: bad, namespace: development}\n",
		"apiVersion: apps/v1\nkind: ConfigMap\nmetadata: {name: bad, namespace: development}\n",
	} {
		seed := seedsFor("nginx")
		seed.ManifestTemplate = manifest
		_, err := render(seed, d.Instance{ID: "i", ResourceName: "n", Namespace: "development", Software: "nginx"}, seed.Defaults, 1)
		require.Error(t, err)
	}
}
func TestSoftwareAliasNormalization(t *testing.T) {
	for alias, want := range map[string]string{" PG ": "postgres", "PostgreSQL": "postgres", "mongo": "mongodb", "MongoDB": "mongodb", "python": "sandbox", "代码沙箱": "sandbox"} {
		require.Equal(t, want, d.NormalizeSoftware(alias))
		profile, ok := d.Profile(alias)
		require.True(t, ok)
		require.Equal(t, want, profile.Software)
	}
	_, ok := d.Profile("arbitrary-program")
	require.False(t, ok)
}
