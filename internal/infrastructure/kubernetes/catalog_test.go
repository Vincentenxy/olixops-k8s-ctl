package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

func fixture() (*Client, *kubefake.Clientset, *dynamicfake.FakeDynamicClient, d.Gateway) {
	g := d.Gateway{ID: "higress", Cluster: "test", Namespace: "higress-system", Service: "higress-gateway", Name: "higress-tcp-gateway", PortStart: 30000, PortEnd: 30200}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: g.Service, Namespace: g.Namespace, Annotations: map[string]string{"keep": "yes"}}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, ClusterIP: "10.0.0.1", Ports: []corev1.ServicePort{{Name: "redis-existing", Port: 30000, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(30000), NodePort: 30000}, {Name: "https", Port: 443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(8443)}}}}
	gw := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway", "metadata": map[string]any{"name": g.Name, "namespace": g.Namespace}, "spec": map[string]any{"gatewayClassName": "higress", "listeners": []any{map[string]any{"name": "old-redis", "port": int64(30000), "protocol": "TCP", "allowedRoutes": map[string]any{"namespaces": map[string]any{"from": "All"}}}}}}}
	route := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1alpha2", "kind": "TCPRoute", "metadata": map[string]any{"name": "redis-route", "namespace": "external"}, "spec": map[string]any{"parentRefs": []any{map[string]any{"name": g.Name, "namespace": g.Namespace, "port": int64(30000)}}}}}
	unstructured.SetNestedSlice(route.Object, []any{map[string]any{"backendRefs": []any{map[string]any{"name": "redis-master", "port": int64(6379)}}}}, "spec", "rules")
	core := kubefake.NewSimpleClientset(svc)
	disc := &fake.FakeDiscovery{Fake: &ktesting.Fake{Resources: []*metav1.APIResourceList{{GroupVersion: "gateway.networking.k8s.io/v1", APIResources: []metav1.APIResource{{Name: "gateways", Kind: "Gateway", Namespaced: true}}}, {GroupVersion: "gateway.networking.k8s.io/v1alpha2", APIResources: []metav1.APIResource{{Name: "tcproutes", Kind: "TCPRoute", Namespaced: true}}}, {GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "configmaps", Kind: "ConfigMap", Namespaced: true}, {Name: "services", Kind: "Service", Namespaced: true}, {Name: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true}}}}}}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{{Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tcproutes"}: "TCPRouteList"}, route)
	if err := dyn.Tracker().Create(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}, gw, g.Namespace); err != nil {
		panic(err)
	}
	return &Client{core: core, dynamic: dyn, discovery: disc}, core, dyn, g
}
func TestCatalogGatewayPreservesResourcesAndRetriesConflicts(t *testing.T) {
	c, core, dyn, g := fixture()
	ctx := context.Background()
	attempts := 0
	core.PrependReactor("update", "services", func(action ktesting.Action) (bool, runtime.Object, error) {
		attempts++
		if attempts == 1 {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "services"}, g.Service, fmt.Errorf("changed"))
		}
		return false, nil, nil
	})
	require.NoError(t, c.CatalogEnsurePort(ctx, g, 30032, "olixops-tcp-30032"))
	require.GreaterOrEqual(t, attempts, 2)
	svc, err := core.CoreV1().Services(g.Namespace).Get(ctx, g.Service, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, svc.Spec.Ports, 3)
	require.Equal(t, int32(30000), svc.Spec.Ports[0].NodePort)
	require.Equal(t, intstr.FromInt(8443), svc.Spec.Ports[1].TargetPort)
	require.Equal(t, "yes", svc.Annotations["keep"])
	require.Equal(t, "10.0.0.1", svc.Spec.ClusterIP)
	gw, err := dyn.Resource(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}).Namespace(g.Namespace).Get(ctx, g.Name, metav1.GetOptions{})
	require.NoError(t, err)
	listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	require.Len(t, listeners, 2)
	require.Equal(t, "old-redis", listeners[0].(map[string]any)["name"])
	require.NoError(t, c.CatalogEnsurePort(ctx, g, 30032, "olixops-tcp-30032"))
	svc, _ = core.CoreV1().Services(g.Namespace).Get(ctx, g.Service, metav1.GetOptions{})
	require.Len(t, svc.Spec.Ports, 3)
	snap, err := c.CatalogSnapshot(ctx, g)
	require.NoError(t, err)
	require.Equal(t, "v1alpha2", snap.TCPRouteVersion)
	require.Contains(t, snap.Ports[30000].Refs, "external/redis-route")
	require.Equal(t, []string{""}, snap.Ports[30000].Owners)
	require.Equal(t, "redis-master", snap.Ports[30000].Mappings[0].Service)
	require.Equal(t, 6379, snap.Ports[30000].Mappings[0].BackendPort)
	require.Equal(t, "olixops-tcp-30032", snap.Ports[30032].Listener)
	_, err = dyn.Resource(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tcproutes"}).Namespace("elsewhere").Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1alpha2", "kind": "TCPRoute", "metadata": map[string]any{"name": "orphan", "namespace": "elsewhere"}, "spec": map[string]any{"parentRefs": []any{map[string]any{"name": g.Name, "namespace": g.Namespace, "port": int64(30040)}}}}}, metav1.CreateOptions{})
	require.NoError(t, err)
	snap, err = c.CatalogSnapshot(ctx, g)
	require.NoError(t, err)
	require.Contains(t, snap.Ports[30040].Refs, "elsewhere/orphan", "a declared route protects even a not-yet-created listener")
}
func TestCatalogDeletionUsesPinnedUIDAndForbiddenKind(t *testing.T) {
	c, _, _, _ := fixture()
	ctx := context.Background()
	uid := types.UID("pinned")
	deletePath := "/api/v1/namespaces/demo/configmaps/config"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, deletePath, r.URL.Path)
		var options metav1.DeleteOptions
		require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
		require.NotNil(t, options.Preconditions)
		require.Equal(t, uid, *options.Preconditions.UID)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Success"}`)
	}))
	defer server.Close()
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	c.dynamic = dyn
	require.NoError(t, c.CatalogDelete(ctx, d.Resource{Kind: "ConfigMap", Namespace: "demo", Name: "config", UID: string(uid)}))
	c.discovery.(*fake.FakeDiscovery).Resources = append(c.discovery.(*fake.FakeDiscovery).Resources, &metav1.APIResourceList{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{{Name: "deployments", Kind: "Deployment", Namespaced: true}}})
	deletePath = "/apis/apps/v1/namespaces/demo/deployments/web"
	require.NoError(t, c.CatalogDelete(ctx, d.Resource{Kind: "Deployment", Namespace: "demo", Name: "web", UID: string(uid)}))
	require.Error(t, c.CatalogDelete(ctx, d.Resource{Kind: "Gateway", Namespace: "higress-system", Name: "shared", UID: "x"}))
	require.Error(t, c.CatalogDelete(ctx, d.Resource{Kind: "ConfigMap", Namespace: "demo", Name: "config"}))
}
func TestCatalogSnapshotFailsClosedOnIncompleteRouteRead(t *testing.T) {
	c, _, dyn, g := fixture()
	dyn.PrependReactor("list", "tcproutes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "tcproutes"}, "", fmt.Errorf("no list"))
	})
	_, err := c.CatalogSnapshot(context.Background(), g)
	require.Error(t, err)
}

func TestCatalogRedisVerificationUsesLBAndSecretWithoutMutatingKubernetes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	password := "test-password-only"
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		auth := fmt.Sprintf("*2\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n", len(password), password)
		got := make([]byte, len(auth))
		if _, err = io.ReadFull(conn, got); err != nil {
			done <- err
			return
		}
		if string(got) != auth {
			done <- fmt.Errorf("unexpected AUTH framing")
			return
		}
		if _, err = io.WriteString(conn, "+OK\r\n"); err != nil {
			done <- err
			return
		}
		ping := "*1\r\n$4\r\nPING\r\n"
		got = make([]byte, len(ping))
		if _, err = io.ReadFull(conn, got); err != nil {
			done <- err
			return
		}
		if string(got) != ping {
			done <- fmt.Errorf("unexpected PING")
			return
		}
		_, err = io.WriteString(conn, "+PONG\r\n")
		done <- err
	}()
	core := kubefake.NewSimpleClientset(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "gateway", Namespace: "higress-system"}, Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "127.0.0.1"}}}}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "alice-redis", Namespace: "demo"}, Data: map[string][]byte{"password": []byte(password)}})
	c := &Client{core: core}
	out, err := c.CatalogVerify(context.Background(), d.Gateway{Namespace: "higress-system", Service: "gateway"}, d.Deployment{Port: port}, d.Instance{Namespace: "demo", Software: "redis"}, d.Parameters{SecretName: "alice-redis", SecretKey: "password"})
	require.NoError(t, err)
	require.NoError(t, <-done)
	require.True(t, out.TCPVerified)
	require.True(t, out.ProtocolVerified)
	require.False(t, out.UserNetworkVerified)
	for _, action := range core.Actions() {
		require.Equal(t, "get", action.GetVerb())
	}
}
