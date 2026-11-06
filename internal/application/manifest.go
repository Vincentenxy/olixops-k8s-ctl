package application

import (
	"io"
	"olixops-k8s-ctl/internal/domain/fault"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/yaml"

	"olixops-k8s-ctl/internal/domain"
)

type ManifestResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

func ValidateManifest(manifest, requestedNamespace string, allowedNamespaces map[string]struct{}) ([]ManifestResource, error) {
	if len(validation.IsDNS1123Label(requestedNamespace)) > 0 {
		return nil, fault.Invalid("invalid namespace")
	}
	if strings.TrimSpace(manifest) == "" {
		return nil, fault.Invalid("manifest is required")
	}
	if len(manifest) > 2<<20 {
		return nil, fault.Invalid("manifest exceeds 2 MiB")
	}
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	resources := make([]ManifestResource, 0, 8)
	for index := 0; ; index++ {
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fault.Invalid("invalid manifest document %d", index+1)
		}
		if len(object) == 0 {
			continue
		}
		item := unstructured.Unstructured{Object: object}
		apiVersion := item.GetAPIVersion()
		kind := item.GetKind()
		name := item.GetName()
		if apiVersion == "" || kind == "" || name == "" {
			return nil, fault.Invalid("manifest document %d requires apiVersion, kind and metadata.name", index+1)
		}
		if kind == "Secret" {
			return nil, fault.Forbidden("Secret manifests are not accepted; use a managed Secret reference")
		}
		if _, ok := domain.LookupResource(kind); !ok {
			return nil, fault.Forbidden("manifest kind is not allowed")
		}
		if !domain.ValidAPIVersion(kind, apiVersion) {
			return nil, fault.Invalid("unsupported apiVersion for %s", kind)
		}
		if len(validation.IsDNS1123Subdomain(name)) > 0 {
			return nil, fault.Invalid("invalid metadata.name")
		}
		namespace := item.GetNamespace()
		if namespace == "" {
			namespace = requestedNamespace
		}
		if namespace != requestedNamespace {
			return nil, fault.Invalid("manifest resource %s/%s targets namespace %q, expected %q", kind, name, namespace, requestedNamespace)
		}
		if len(allowedNamespaces) > 0 {
			if _, ok := allowedNamespaces[namespace]; !ok {
				return nil, fault.Forbidden("manifest namespace is not allowed")
			}
		}
		resources = append(resources, ManifestResource{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name})
	}
	if len(resources) == 0 {
		return nil, fault.Invalid("manifest contains no resources")
	}
	return resources, nil
}
