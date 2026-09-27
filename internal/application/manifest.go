package application

import (
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

type ManifestResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

var allowedManifestKinds = map[string]struct{}{
	"ConfigMap":             {},
	"Deployment":            {},
	"Gateway":               {},
	"HTTPRoute":             {},
	"PersistentVolumeClaim": {},
	"Service":               {},
	"StatefulSet":           {},
	"TCPRoute":              {},
}

func ValidateManifest(manifest, requestedNamespace string, allowedNamespaces map[string]struct{}) ([]ManifestResource, error) {
	if strings.TrimSpace(manifest) == "" {
		return nil, fmt.Errorf("manifest is required")
	}
	if len(manifest) > 2<<20 {
		return nil, fmt.Errorf("manifest exceeds 2 MiB")
	}
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
	resources := make([]ManifestResource, 0, 8)
	for index := 0; ; index++ {
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode manifest document %d: %w", index+1, err)
		}
		if len(object) == 0 {
			continue
		}
		item := unstructured.Unstructured{Object: object}
		apiVersion := item.GetAPIVersion()
		kind := item.GetKind()
		name := item.GetName()
		if apiVersion == "" || kind == "" || name == "" {
			return nil, fmt.Errorf("manifest document %d requires apiVersion, kind and metadata.name", index+1)
		}
		if kind == "Secret" {
			return nil, fmt.Errorf("Secret manifests are not accepted; use a managed Secret reference")
		}
		if _, ok := allowedManifestKinds[kind]; !ok {
			return nil, fmt.Errorf("manifest kind %q is not allowed", kind)
		}
		namespace := item.GetNamespace()
		if namespace == "" {
			namespace = requestedNamespace
		}
		if namespace != requestedNamespace {
			return nil, fmt.Errorf("manifest resource %s/%s targets namespace %q, expected %q", kind, name, namespace, requestedNamespace)
		}
		if len(allowedNamespaces) > 0 {
			if _, ok := allowedNamespaces[namespace]; !ok {
				return nil, fmt.Errorf("manifest namespace %q is not allowed", namespace)
			}
		}
		resources = append(resources, ManifestResource{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name})
	}
	if len(resources) == 0 {
		return nil, fmt.Errorf("manifest contains no resources")
	}
	return resources, nil
}
