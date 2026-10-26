package kubernetes

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

func (c *Client) catalogRoutes(ctx context.Context) (dynamic.NamespaceableResourceInterface, string, error) {
	for _, ver := range []string{"v1", "v1alpha2"} {
		api := "gateway.networking.k8s.io/" + ver
		r, err := c.discovery.ServerResourcesForGroupVersion(api)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		for _, v := range r.APIResources {
			if v.Name == "tcproutes" {
				return c.dynamic.Resource(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: ver, Resource: "tcproutes"}), ver, nil
			}
		}
	}
	return nil, "", d.Fail("GATEWAY_API_MISSING", "TCPRoute API is not served")
}
func (c *Client) CatalogSnapshot(ctx context.Context, g d.Gateway) (d.Snapshot, error) {
	out := d.Snapshot{Ports: map[int]d.PortUsage{}}
	for port := g.PortStart; port <= g.PortEnd; port++ {
		out.Ports[port] = d.PortUsage{Port: port}
	}
	svc, err := c.core.CoreV1().Services(g.Namespace).Get(ctx, g.Service, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return out, d.Fail("GATEWAY_CONFIGURATION", "configured ingress Service is not LoadBalancer")
	}
	gwres, err := c.resource(ctx, "Gateway", g.Namespace)
	if err != nil {
		return out, err
	}
	gw, err := gwres.Get(ctx, g.Name, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	routes, ver, err := c.catalogRoutes(ctx)
	if err != nil {
		return out, err
	}
	out.TCPRouteVersion = ver
	for _, p := range svc.Spec.Ports {
		if p.Protocol == corev1.ProtocolTCP || p.Protocol == "" {
			u := out.Ports[int(p.Port)]
			u.Port = int(p.Port)
			u.Exposed = true
			out.Ports[u.Port] = u
		}
	}
	listeners, _, err := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	if err != nil {
		return out, err
	}
	names := map[string]int{}
	for _, raw := range listeners {
		v, ok := raw.(map[string]any)
		if !ok {
			return out, fmt.Errorf("invalid gateway listener")
		}
		port, _, _ := unstructured.NestedInt64(v, "port")
		name, _, _ := unstructured.NestedString(v, "name")
		proto, _, _ := unstructured.NestedString(v, "protocol")
		if proto != "TCP" {
			continue
		}
		u := out.Ports[int(port)]
		if u.Listener != "" && u.Listener != name {
			return out, d.Fail("GATEWAY_CONFIGURATION", "multiple TCP listeners share an external port")
		}
		u.Port = int(port)
		u.Listener = name
		out.Ports[u.Port] = u
		names[name] = int(port)
	}
	// All namespaces, with pagination: a route outside the target namespace can
	// still consume a shared listener. Incomplete reads cannot produce free ports.
	token := ""
	for {
		list, err := routes.List(ctx, metav1.ListOptions{Limit: 200, Continue: token})
		if err != nil {
			return out, err
		}
		for _, route := range list.Items {
			parents, _, e := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
			if e != nil {
				return out, e
			}
			for _, raw := range parents {
				parent, ok := raw.(map[string]any)
				if !ok {
					return out, fmt.Errorf("invalid route parent")
				}
				name, _, _ := unstructured.NestedString(parent, "name")
				ns, _, _ := unstructured.NestedString(parent, "namespace")
				if ns == "" {
					ns = route.GetNamespace()
				}
				group, _, _ := unstructured.NestedString(parent, "group")
				kind, _, _ := unstructured.NestedString(parent, "kind")
				if name != g.Name || ns != g.Namespace || group != "" && group != "gateway.networking.k8s.io" || kind != "" && kind != "Gateway" {
					continue
				}
				port, hasPort, _ := unstructured.NestedInt64(parent, "port")
				section, hasSection, _ := unstructured.NestedString(parent, "sectionName")
				for p, u := range out.Ports {

					if hasPort && int(port) != p {
						continue
					}
					if hasSection && names[section] != p && section != fmt.Sprintf("olixops-tcp-%d", p) {
						continue
					}
					u.Refs = append(u.Refs, route.GetNamespace()+"/"+route.GetName())
					u.Owners = append(u.Owners, route.GetLabels()[d.InstanceLabel])
					rules, _, err := unstructured.NestedSlice(route.Object, "spec", "rules")
					if err != nil {
						return out, err
					}
					for _, rawRule := range rules {
						rule := rawRule.(map[string]any)
						backends, _, err := unstructured.NestedSlice(rule, "backendRefs")
						if err != nil {
							return out, err
						}
						for _, rawBackend := range backends {
							b := rawBackend.(map[string]any)
							service, _, _ := unstructured.NestedString(b, "name")
							ns, _, _ := unstructured.NestedString(b, "namespace")
							if ns == "" {
								ns = route.GetNamespace()
							}
							kind, _, _ := unstructured.NestedString(b, "kind")
							if kind == "" {
								kind = "Service"
							}
							backendPort, _, _ := unstructured.NestedInt64(b, "port")
							u.Mappings = append(u.Mappings, d.RouteMapping{Namespace: route.GetNamespace(), Route: route.GetName(), Service: service, ServiceNamespace: ns, BackendKind: kind, BackendPort: int(backendPort), InstanceID: route.GetLabels()[d.InstanceLabel]})
						}
					}
					out.Ports[p] = u
				}
			}
		}
		token = list.GetContinue()
		if token == "" {
			break
		}
	}
	return out, nil
}
func (c *Client) CatalogEnsurePort(ctx context.Context, g d.Gateway, port int, name string) error {
	// Update only one array item while preserving the live object and its RV.
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		svc, err := c.core.CoreV1().Services(g.Namespace).Get(ctx, g.Service, metav1.GetOptions{})
		if err != nil {
			return err
		}
		for _, p := range svc.Spec.Ports {
			if p.Port == int32(port) {
				if p.Name != name || p.Protocol != corev1.ProtocolTCP || p.TargetPort != intstr.FromInt(port) {
					return d.Fail("PORT_IN_USE", "existing Service port has a different protocol or targetPort")
				}
				return nil
			}
			if p.Name == name {
				return d.Fail("PORT_IN_USE", "Service port name already exists")
			}
		}
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{Name: name, Protocol: corev1.ProtocolTCP, Port: int32(port), TargetPort: intstr.FromInt(port)})
		_, err = c.core.CoreV1().Services(g.Namespace).Update(ctx, svc, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return err
	}
	res, err := c.resource(ctx, "Gateway", g.Namespace)
	if err != nil {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gw, err := res.Get(ctx, g.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		listeners, _, err := unstructured.NestedSlice(gw.Object, "spec", "listeners")
		if err != nil {
			return err
		}
		for _, raw := range listeners {
			v := raw.(map[string]any)
			p, _, _ := unstructured.NestedInt64(v, "port")
			n, _, _ := unstructured.NestedString(v, "name")
			proto, _, _ := unstructured.NestedString(v, "protocol")
			if p == int64(port) || n == name {
				if p != int64(port) || n != name || proto != "TCP" {
					return d.Fail("PORT_IN_USE", "Gateway listener already exists with conflicting settings")
				}
				from, _, _ := unstructured.NestedString(v, "allowedRoutes", "namespaces", "from")
				if from != "All" {
					return d.Fail("GATEWAY_CONFIGURATION", "existing managed listener must allow instance namespaces")
				}
				return nil
			}
		}
		listeners = append(listeners, map[string]any{"name": name, "port": int64(port), "protocol": "TCP", "allowedRoutes": map[string]any{"namespaces": map[string]any{"from": "All"}, "kinds": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "TCPRoute"}}}})
		if err = unstructured.SetNestedSlice(gw.Object, listeners, "spec", "listeners"); err != nil {
			return err
		}
		_, err = res.Update(ctx, gw, metav1.UpdateOptions{})
		return err
	})
}

// CatalogNamespaceExists preserves NotFound as the only createable state.
// An inaccessible or terminating namespace must never be treated as missing.
func (c *Client) CatalogNamespaceExists(ctx context.Context, name string) (bool, error) {
	ns, err := c.core.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, d.Fail("NAMESPACE_UNAVAILABLE", "cannot read the requested Namespace; check permissions and cluster connectivity")
	}
	if ns.DeletionTimestamp != nil || ns.Status.Phase == corev1.NamespaceTerminating {
		return false, d.Fail("NAMESPACE_TERMINATING", "requested Namespace is terminating; wait for deletion or specify another Namespace")
	}
	return true, nil
}

// CatalogPreflightResources checks storage/credentials after the application has
// ensured the Namespace exists. It does not repeat that Namespace lookup.
func (c *Client) CatalogPreflightResources(ctx context.Context, i d.Instance, p d.Parameters) error {
	profile, ok := d.Profile(i.Software)
	if !ok {
		return d.Fail("UNSUPPORTED_SOFTWARE", "standard service software is not supported")
	}
	if profile.Persistent {
		_, err := c.core.StorageV1().StorageClasses().Get(ctx, p.StorageClass, metav1.GetOptions{})
		if err != nil {
			return d.Fail("STORAGECLASS_UNAVAILABLE", "configured StorageClass is unavailable")
		}
	}
	if p.SecretName == "" {
		return nil
	}
	secret, err := c.core.CoreV1().Secrets(i.Namespace).Get(ctx, p.SecretName, metav1.GetOptions{})
	if err != nil {
		return d.Fail("SECRET_UNAVAILABLE", "create the referenced Secret in the instance Namespace")
	}
	if len(secret.Data[p.SecretKey]) == 0 {
		return d.Fail("SECRET_KEY_MISSING", "configured Secret key is absent or empty")
	}
	return nil
}
func catalogObjects(raw string) ([]*unstructured.Unstructured, error) {
	dec := kyaml.NewYAMLOrJSONDecoder(strings.NewReader(raw), 4096)
	var out []*unstructured.Unstructured
	for {
		var obj map[string]any
		err := dec.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(obj) > 0 {
			out = append(out, &unstructured.Unstructured{Object: obj})
		}
	}
	return out, nil
}
func (c *Client) CatalogInventory(ctx context.Context, raw string) ([]d.Resource, error) {
	objs, err := catalogObjects(raw)
	if err != nil {
		return nil, err
	}
	out := []d.Resource{}
	for _, obj := range objs {
		res, err := c.resource(ctx, obj.GetKind(), obj.GetNamespace())
		if err != nil {
			return nil, err
		}
		live, err := res.Get(ctx, obj.GetName(), metav1.GetOptions{})
		r := d.Resource{APIVersion: obj.GetAPIVersion(), Kind: obj.GetKind(), Namespace: obj.GetNamespace(), Name: obj.GetName()}
		if apierrors.IsNotFound(err) {
			out = append(out, r)
			continue
		}
		if err != nil {
			return nil, err
		}
		if obj.GetLabels()[d.InstanceLabel] == "" || live.GetLabels()[d.InstanceLabel] != obj.GetLabels()[d.InstanceLabel] {
			return nil, d.Fail("RESOURCE_OWNERSHIP_CONFLICT", "an existing resource does not belong to this instance")
		}
		r.UID = string(live.GetUID())
		out = append(out, r)
	}
	return out, nil
}
func (c *Client) CatalogDelete(ctx context.Context, r d.Resource) error {
	if r.UID == "" {
		return d.Fail("RESOURCE_IDENTITY_MISSING", "deletion requires a recorded UID")
	}
	switch r.Kind {
	case "TCPRoute", "StatefulSet", "Deployment", "Service", "ConfigMap":
	default:
		return d.Fail("RESOURCE_DELETE_FORBIDDEN", "only owned instance workloads, Services, ConfigMaps and routes can be deleted")
	}
	res, err := c.resource(ctx, r.Kind, r.Namespace)
	if err != nil {
		return err
	}
	uid := types.UID(r.UID)
	prop := metav1.DeletePropagationForeground
	err = res.Delete(ctx, r.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}, PropagationPolicy: &prop})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
func conditionsTrue(obj map[string]any, generation int64, names ...string) bool {
	conditions, _, _ := unstructured.NestedSlice(obj, "conditions")
	for _, name := range names {
		found := false
		for _, raw := range conditions {
			v, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			t, _, _ := unstructured.NestedString(v, "type")
			status, _, _ := unstructured.NestedString(v, "status")
			observed, ok, _ := unstructured.NestedInt64(v, "observedGeneration")
			if t == name && status == "True" && ok && observed >= generation {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (c *Client) CatalogInspect(ctx context.Context, dep d.Deployment, g d.Gateway) (d.Observation, error) {
	out := d.Observation{Resources: []d.Resource{}, Missing: []d.Resource{}, WorkloadReady: true, PVCBound: true, RouteReady: true, Note: "Readiness is a Kubernetes snapshot. External TCP, database protocol and user-network access have not been verified."}
	inv, err := c.CatalogInventory(ctx, dep.YAML)
	if err != nil {
		return out, err
	}
	out.Resources = inv
	workloadCount, routeCount := 0, 0
	for _, r := range inv {
		for _, old := range dep.Resources {
			if r.Kind == old.Kind && r.Namespace == old.Namespace && r.Name == old.Name && old.UID != "" && r.UID != "" && old.UID != r.UID {
				return out, d.Fail("RESOURCE_IDENTITY_CONFLICT", "live resource UID differs from recorded UID")
			}
		}
		if r.UID == "" {
			out.Missing = append(out.Missing, r)
		}
		switch r.Kind {
		case "StatefulSet", "Deployment":
			workloadCount++
			if r.UID == "" {
				out.WorkloadReady = false
				continue
			}
			status, err := c.GetRolloutStatus(ctx, r.Namespace, r.Kind, r.Name)
			if err != nil {
				return out, err
			}
			out.WorkloadReady = out.WorkloadReady && status.Complete
		case "PersistentVolumeClaim":
			if r.UID == "" {
				out.PVCBound = false
				continue
			}
			pvc, err := c.core.CoreV1().PersistentVolumeClaims(r.Namespace).Get(ctx, r.Name, metav1.GetOptions{})
			if err != nil {
				return out, err
			}
			out.PVCBound = out.PVCBound && pvc.Status.Phase == corev1.ClaimBound
		case "TCPRoute":
			routeCount++
			if r.UID == "" {
				out.RouteReady = false
				continue
			}
			res, err := c.resource(ctx, r.Kind, r.Namespace)
			if err != nil {
				return out, err
			}
			obj, err := res.Get(ctx, r.Name, metav1.GetOptions{})
			if err != nil {
				return out, err
			}
			parents, _, _ := unstructured.NestedSlice(obj.Object, "status", "parents")
			matched := false
			for _, raw := range parents {
				v := raw.(map[string]any)
				parent, _, _ := unstructured.NestedMap(v, "parentRef")
				name, _, _ := unstructured.NestedString(parent, "name")
				ns, _, _ := unstructured.NestedString(parent, "namespace")
				if ns == "" {
					ns = r.Namespace
				}
				section, hasSection, _ := unstructured.NestedString(parent, "sectionName")
				port, hasPort, _ := unstructured.NestedInt64(parent, "port")
				if name == g.Name && ns == g.Namespace && (!hasSection || section == fmt.Sprintf("olixops-tcp-%d", dep.Port)) && (!hasPort || port == int64(dep.Port)) && conditionsTrue(v, obj.GetGeneration(), "Accepted", "ResolvedRefs") {
					matched = true
				}
			}
			out.RouteReady = out.RouteReady && matched
		}
	}
	out.WorkloadReady = out.WorkloadReady && workloadCount > 0
	out.RouteReady = out.RouteReady && routeCount > 0
	res, err := c.resource(ctx, "Gateway", g.Namespace)
	if err != nil {
		return out, err
	}
	gw, err := res.Get(ctx, g.Name, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	status, _, _ := unstructured.NestedMap(gw.Object, "status")
	out.GatewayReady = conditionsTrue(status, gw.GetGeneration(), "Accepted", "Programmed")
	listenerReady := false
	listeners, _, _ := unstructured.NestedSlice(status, "listeners")
	for _, raw := range listeners {
		v := raw.(map[string]any)
		name, _, _ := unstructured.NestedString(v, "name")
		if name == fmt.Sprintf("olixops-tcp-%d", dep.Port) && conditionsTrue(v, gw.GetGeneration(), "Accepted", "Programmed", "ResolvedRefs") {
			listenerReady = true
		}
	}
	out.GatewayReady = out.GatewayReady && listenerReady
	svc, err := c.core.CoreV1().Services(g.Namespace).Get(ctx, g.Service, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		entry := svc.Status.LoadBalancer.Ingress[0]
		host := entry.IP
		if host == "" {
			host = entry.Hostname
		}
		if host != "" {
			out.Address = net.JoinHostPort(host, strconv.Itoa(dep.Port))
		}
	}
	return out, nil
}
