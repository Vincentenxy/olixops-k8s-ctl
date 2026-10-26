package application

import (
	"context"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"olixops-k8s-ctl/internal/domain"
	d "olixops-k8s-ctl/internal/domain/catalog"
	"strings"
)

// CatalogCluster is the restricted bridge between catalog workflows and existing
// cluster policy, Provider-backed clients and argv-only kubectl execution.
type CatalogCluster struct{ Service *ClusterService }

func (a CatalogCluster) Check(ctx context.Context, raw, ns string) error {
	resources, err := a.Service.ValidateManifest(ctx, raw, ns)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, r := range resources {
		switch r.Kind {
		case "StatefulSet", "Deployment", "Service", "PersistentVolumeClaim", "ConfigMap", "TCPRoute":
			counts[r.Kind]++
		default:
			return d.Fail("INVALID_TEMPLATE", "standard templates allow only StatefulSet, Deployment, Service, PVC, ConfigMap and TCPRoute")
		}
	}
	if counts["StatefulSet"]+counts["Deployment"] != 1 || counts["Service"] < 1 || counts["Service"] > 1+counts["StatefulSet"] || counts["PersistentVolumeClaim"] > 1 || counts["ConfigMap"] > 1 || counts["TCPRoute"] > 1 {
		return d.Fail("INVALID_TEMPLATE", "standard template requires one workload, one backend Service, at most one governing StatefulSet Service, PVC, ConfigMap and generated TCPRoute")
	}
	if counts["StatefulSet"] == 1 && counts["PersistentVolumeClaim"] != 1 || counts["Deployment"] == 1 && counts["PersistentVolumeClaim"] != 0 {
		return d.Fail("INVALID_TEMPLATE", "StatefulSet templates require one PVC; Deployment templates use ephemeral storage")
	}
	return nil
}
func (a CatalogCluster) Snapshot(ctx context.Context, g d.Gateway) (d.Snapshot, error) {
	if err := a.Service.requireNamespace(g.Namespace); err != nil {
		return d.Snapshot{}, err
	}
	if err := a.Service.authorize(ctx, "catalog.gateway.snapshot", false, []domain.Target{{Kind: "Service", Namespace: g.Namespace, Name: g.Service}, {Kind: "Gateway", Namespace: g.Namespace, Name: g.Name}, {Kind: "TCPRoute", Namespace: "*"}}, g); err != nil {
		return d.Snapshot{}, err
	}
	return a.Service.client.CatalogSnapshot(ctx, g)
}

// ensureNamespace is part of Preflight after its namespace scope/read policy check.
// Shared namespaces never enter instance inventory.
func (a CatalogCluster) ensureNamespace(ctx context.Context, namespace string) error {
	exists, err := a.Service.client.CatalogNamespaceExists(ctx, namespace)
	if err != nil || exists {
		return err
	}
	if _, err = a.Service.CreateNamespace(ctx, namespace); err != nil && !apierrors.IsAlreadyExists(err) {
		return d.Fail("NAMESPACE_CREATION_FAILED", "cannot create the requested Namespace; check Kubernetes write settings and namespace creation permissions, then retry")
	}
	exists, err = a.Service.client.CatalogNamespaceExists(ctx, namespace)
	if err != nil {
		return err
	}
	if !exists {
		return d.Fail("NAMESPACE_UNAVAILABLE", "Namespace creation is not yet observable; retry the same deployment")
	}
	return nil
}
func (a CatalogCluster) Preflight(ctx context.Context, i d.Instance, p d.Parameters) error {
	if err := a.Service.requireNamespace(i.Namespace); err != nil {
		return err
	}
	profile, ok := d.Profile(i.Software)
	if !ok {
		return d.Fail("UNSUPPORTED_SOFTWARE", "standard service software is not supported")
	}
	targets := []domain.Target{{Kind: "Namespace", Name: i.Namespace}}
	if profile.Persistent {
		targets = append(targets, domain.Target{Kind: "StorageClass", Name: p.StorageClass})
	}
	if p.SecretName != "" {
		targets = append(targets, domain.Target{Kind: "Secret", Namespace: i.Namespace, Name: p.SecretName})
	}
	if err := a.Service.authorize(ctx, "catalog.preflight", false, targets, i); err != nil {
		return err
	}
	if err := a.ensureNamespace(ctx, i.Namespace); err != nil {
		return err
	}
	return a.Service.client.CatalogPreflightResources(ctx, i, p)
}
func (a CatalogCluster) EnsurePort(ctx context.Context, g d.Gateway, p int, name string) error {
	if p < g.PortStart || p > g.PortEnd {
		return d.Fail("PORT_NOT_IN_POOL", "external port is outside the configured pool")
	}
	if err := a.Service.requireNamespace(g.Namespace); err != nil {
		return err
	}
	if err := a.Service.authorize(ctx, "catalog.gateway.expose", true, []domain.Target{{Kind: "Service", Namespace: g.Namespace, Name: g.Service}, {Kind: "Gateway", Namespace: g.Namespace, Name: g.Name}}, []any{g, p, name}); err != nil {
		return err
	}
	return a.Service.client.CatalogEnsurePort(ctx, g, p, name)
}
func (a CatalogCluster) Apply(ctx context.Context, raw, ns string, dry bool) error {
	if err := a.Check(ctx, raw, ns); err != nil {
		return err
	}
	_, _, err := a.Service.ApplyManifest(ctx, raw, ns, true, dry)
	return err
}
func (a CatalogCluster) Inventory(ctx context.Context, raw string) ([]d.Resource, error) {
	// The manifest was frozen and locally validated before deployment. Inventory
	// revalidates every namespace; no resource/connection is supplied by the caller.
	// ValidateManifest requires a concrete namespace, so derive it using a fixed
	// manifest field and then run the same application validation.
	ns, err := catalogManifestNamespace(raw)
	if err != nil {
		return nil, err
	}
	if err = a.Check(ctx, raw, ns); err != nil {
		return nil, err
	}
	return a.Service.client.CatalogInventory(ctx, raw)
}
func (a CatalogCluster) Inspect(ctx context.Context, dep d.Deployment, g d.Gateway) (d.Observation, error) {
	ns, err := catalogManifestNamespace(dep.YAML)
	if err != nil {
		return d.Observation{}, err
	}
	if err = a.Check(ctx, dep.YAML, ns); err != nil {
		return d.Observation{}, err
	}
	if err = a.Service.requireNamespace(g.Namespace); err != nil {
		return d.Observation{}, err
	}
	return a.Service.client.CatalogInspect(ctx, dep, g)
}
func (a CatalogCluster) Delete(ctx context.Context, r d.Resource) error {
	if err := a.Service.requireNamespace(r.Namespace); err != nil {
		return err
	}
	if err := a.Service.authorize(ctx, "catalog.resource.delete", true, []domain.Target{{Kind: r.Kind, Namespace: r.Namespace, Name: r.Name}}, r); err != nil {
		return err
	}
	return a.Service.client.CatalogDelete(ctx, r)
}

func catalogManifestNamespace(raw string) (string, error) {
	dec := kyaml.NewYAMLOrJSONDecoder(strings.NewReader(raw), 4096)
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return "", err
	}
	u := &unstructured.Unstructured{Object: obj}
	if u.GetNamespace() == "" {
		return "", d.Fail("INVALID_TEMPLATE", "manifest namespace is required")
	}
	return u.GetNamespace(), nil
}

func (a CatalogCluster) Verify(ctx context.Context, g d.Gateway, dep d.Deployment, i d.Instance, p d.Parameters) (d.AccessReport, error) {
	if err := a.Service.requireNamespace(i.Namespace); err != nil {
		return d.AccessReport{}, err
	}
	if err := a.Service.requireNamespace(g.Namespace); err != nil {
		return d.AccessReport{}, err
	}
	targets := []domain.Target{{Kind: "Service", Namespace: g.Namespace, Name: g.Service}}
	if p.SecretName != "" {
		targets = append(targets, domain.Target{Kind: "Secret", Namespace: i.Namespace, Name: p.SecretName})
	}
	if err := a.Service.authorize(ctx, "catalog.access.verify", false, targets, []any{dep.ID, dep.Port}); err != nil {
		return d.AccessReport{}, err
	}
	return a.Service.client.CatalogVerify(ctx, g, dep, i, p)
}
