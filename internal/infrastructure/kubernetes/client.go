package kubernetes

import (
	"context"
	"fmt"
	"time"

	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Client struct {
	core      kubernetes.Interface
	dynamic   dynamic.Interface
	discovery discovery.DiscoveryInterface
	context   string
}

type ClusterStatus struct {
	ServerVersion string `json:"serverVersion"`
	GitVersion    string `json:"gitVersion"`
	Context       string `json:"context,omitempty"`
	Namespaces    int    `json:"namespaceCount"`
}

type NamespaceSummary struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type StorageClassSummary struct {
	Name              string `json:"name"`
	Provisioner       string `json:"provisioner"`
	ReclaimPolicy     string `json:"reclaimPolicy,omitempty"`
	VolumeBindingMode string `json:"volumeBindingMode,omitempty"`
}

type ResourceSummary struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Desired   int32  `json:"desired,omitempty"`
	Ready     int32  `json:"ready,omitempty"`
	Phase     string `json:"phase,omitempty"`
	Type      string `json:"type,omitempty"`
}

type NamespaceResources struct {
	Namespace    string            `json:"namespace"`
	Deployments  []ResourceSummary `json:"deployments"`
	StatefulSets []ResourceSummary `json:"statefulSets"`
	Pods         []ResourceSummary `json:"pods"`
	Services     []ResourceSummary `json:"services"`
	PVCs         []ResourceSummary `json:"persistentVolumeClaims"`
}

type GatewaySnapshot struct {
	Resources []ResourceSummary `json:"resources"`
	Missing   []string          `json:"missingResources,omitempty"`
}

func New(cfg config.KubernetesConfig, provider kubeconfig.Provider) (*Client, error) {
	restConfig, err := buildRESTConfig(cfg, provider)
	if err != nil {
		return nil, err
	}
	// Bound discovery calls as well as ordinary client requests.
	restConfig.Timeout = 30 * time.Second
	core, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create dynamic kubernetes client: %w", err)
	}
	discoveryClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes discovery client: %w", err)
	}
	return &Client{core: core, dynamic: dynamicClient, discovery: discoveryClient, context: cfg.Kubeconfig.Context}, nil
}

func buildRESTConfig(cfg config.KubernetesConfig, provider kubeconfig.Provider) (*rest.Config, error) {
	if provider == nil {
		return nil, fmt.Errorf("kubeconfig provider is not configured")
	}
	document, err := provider.Load(context.Background(), kubeconfig.Reference{
		Source:    cfg.Kubeconfig.Source,
		Path:      cfg.Kubeconfig.Path,
		Reference: cfg.Kubeconfig.Reference,
		Context:   cfg.Kubeconfig.Context,
	})
	if err != nil {
		return nil, err
	}
	rawConfig, err := clientcmd.Load(document.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig from %s: %w", document.Origin, err)
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: cfg.Kubeconfig.Context}
	clientConfig := clientcmd.NewDefaultClientConfig(*rawConfig, overrides)
	result, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	return result, nil
}

func (c *Client) Status(ctx context.Context) (ClusterStatus, error) {
	serverVersion, err := c.discovery.ServerVersion()
	if err != nil {
		return ClusterStatus{}, fmt.Errorf("query kubernetes server version: %w", err)
	}
	namespaces, err := c.core.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterStatus{}, fmt.Errorf("list namespaces: %w", err)
	}
	return ClusterStatus{
		ServerVersion: serverVersion.String(),
		GitVersion:    serverVersion.GitVersion,
		Context:       c.context,
		Namespaces:    len(namespaces.Items),
	}, nil
}

func (c *Client) ListNamespaces(ctx context.Context) ([]NamespaceSummary, error) {
	items, err := c.core.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	result := make([]NamespaceSummary, 0, len(items.Items))
	for _, item := range items.Items {
		result = append(result, NamespaceSummary{Name: item.Name, Status: string(item.Status.Phase)})
	}
	return result, nil
}

func (c *Client) ListStorageClasses(ctx context.Context) ([]StorageClassSummary, error) {
	items, err := c.core.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list storage classes: %w", err)
	}
	result := make([]StorageClassSummary, 0, len(items.Items))
	for _, item := range items.Items {
		summary := StorageClassSummary{Name: item.Name, Provisioner: item.Provisioner}
		if item.ReclaimPolicy != nil {
			summary.ReclaimPolicy = string(*item.ReclaimPolicy)
		}
		if item.VolumeBindingMode != nil {
			summary.VolumeBindingMode = string(*item.VolumeBindingMode)
		}
		result = append(result, summary)
	}
	return result, nil
}

func (c *Client) ListResources(ctx context.Context, namespace string) (NamespaceResources, error) {
	result := NamespaceResources{Namespace: namespace}
	deployments, err := c.core.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, fmt.Errorf("list deployments: %w", err)
	}
	for _, item := range deployments.Items {
		desired := int32(1)
		if item.Spec.Replicas != nil {
			desired = *item.Spec.Replicas
		}
		result.Deployments = append(result.Deployments, ResourceSummary{Kind: "Deployment", Namespace: namespace, Name: item.Name, Desired: desired, Ready: item.Status.ReadyReplicas})
	}
	statefulSets, err := c.core.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, fmt.Errorf("list statefulsets: %w", err)
	}
	for _, item := range statefulSets.Items {
		desired := int32(1)
		if item.Spec.Replicas != nil {
			desired = *item.Spec.Replicas
		}
		result.StatefulSets = append(result.StatefulSets, ResourceSummary{Kind: "StatefulSet", Namespace: namespace, Name: item.Name, Desired: desired, Ready: item.Status.ReadyReplicas})
	}
	pods, err := c.core.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, fmt.Errorf("list pods: %w", err)
	}
	for _, item := range pods.Items {
		result.Pods = append(result.Pods, ResourceSummary{Kind: "Pod", Namespace: namespace, Name: item.Name, Phase: string(item.Status.Phase)})
	}
	services, err := c.core.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, fmt.Errorf("list services: %w", err)
	}
	for _, item := range services.Items {
		result.Services = append(result.Services, ResourceSummary{Kind: "Service", Namespace: namespace, Name: item.Name, Type: string(item.Spec.Type)})
	}
	pvcs, err := c.core.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, fmt.Errorf("list persistent volume claims: %w", err)
	}
	for _, item := range pvcs.Items {
		result.PVCs = append(result.PVCs, ResourceSummary{Kind: "PersistentVolumeClaim", Namespace: namespace, Name: item.Name, Phase: string(item.Status.Phase)})
	}
	return result, nil
}

func (c *Client) ListGatewayResources(ctx context.Context, namespace string) (GatewaySnapshot, error) {
	resources := []struct {
		name       string
		resource   schema.GroupVersionResource
		namespaced bool
	}{
		{name: "GatewayClass", resource: schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses"}},
		{name: "Gateway", resource: schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}, namespaced: true},
		{name: "HTTPRoute", resource: schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}, namespaced: true},
		{name: "TCPRoute", resource: schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tcproutes"}, namespaced: true},
	}
	result := GatewaySnapshot{}
	for _, item := range resources {
		var list *unstructured.UnstructuredList
		var err error
		versions := []string{item.resource.Version}
		for _, version := range []string{"v1", "v1beta1", "v1alpha2"} {
			if version != item.resource.Version {
				versions = append(versions, version)
			}
		}
		for _, version := range versions {
			gvr := item.resource
			gvr.Version = version
			if item.namespaced {
				list, err = c.dynamic.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
			} else {
				list, err = c.dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
			}
			if !errors.IsNotFound(err) && !errors.IsMethodNotSupported(err) {
				break
			}
		}
		if err != nil {
			if errors.IsNotFound(err) || errors.IsMethodNotSupported(err) {
				result.Missing = append(result.Missing, item.name)
				continue
			}
			return result, fmt.Errorf("list %s: %w", item.name, err)
		}
		for _, object := range list.Items {
			result.Resources = append(result.Resources, ResourceSummary{Kind: item.name, Namespace: object.GetNamespace(), Name: object.GetName()})
		}
	}
	return result, nil
}
