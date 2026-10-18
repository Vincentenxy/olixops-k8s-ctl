package domain

// ResourceType is the fixed allowlist used by YAML validation and CRUD adapters.
// Cluster-scoped resources and credentials are deliberately absent.
type ResourceType struct {
	Kind     string
	Group    string
	Versions []string
	Resource string
}

var resourceTypes = []ResourceType{
	{"ConfigMap", "", []string{"v1"}, "configmaps"},
	{"Pod", "", []string{"v1"}, "pods"},
	{"Service", "", []string{"v1"}, "services"},
	{"PersistentVolumeClaim", "", []string{"v1"}, "persistentvolumeclaims"},
	{"Deployment", "apps", []string{"v1"}, "deployments"},
	{"StatefulSet", "apps", []string{"v1"}, "statefulsets"},
	{"DaemonSet", "apps", []string{"v1"}, "daemonsets"},
	{"Job", "batch", []string{"v1"}, "jobs"},
	{"CronJob", "batch", []string{"v1"}, "cronjobs"},
	{"Ingress", "networking.k8s.io", []string{"v1"}, "ingresses"},
	{"NetworkPolicy", "networking.k8s.io", []string{"v1"}, "networkpolicies"},
	{"HorizontalPodAutoscaler", "autoscaling", []string{"v2"}, "horizontalpodautoscalers"},
	{"PodDisruptionBudget", "policy", []string{"v1"}, "poddisruptionbudgets"},
	{"Gateway", "gateway.networking.k8s.io", []string{"v1", "v1beta1"}, "gateways"},
	{"HTTPRoute", "gateway.networking.k8s.io", []string{"v1", "v1beta1"}, "httproutes"},
	{"TCPRoute", "gateway.networking.k8s.io", []string{"v1alpha2", "v1"}, "tcproutes"},
}

func LookupResource(kind string) (ResourceType, bool) {
	for _, r := range resourceTypes {
		if r.Kind == kind {
			return r, true
		}
	}
	return ResourceType{}, false
}

func ValidAPIVersion(kind, apiVersion string) bool {
	r, ok := LookupResource(kind)
	if !ok {
		return false
	}
	for _, version := range r.Versions {
		if r.Group != "" {
			version = r.Group + "/" + version
		}
		if version == apiVersion {
			return true
		}
	}
	return false
}
