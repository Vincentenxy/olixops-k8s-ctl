package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"

	d "olixops-k8s-ctl/internal/domain/catalog"
)

func TestCatalogPreflightRespectsPersistenceAndOptionalCredentials(t *testing.T) {
	for _, software := range []string{"nginx", "sandbox", "redis", "postgres", "mysql", "mongodb", "milvus"} {
		t.Run(software, func(t *testing.T) {
			core := kubefake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "demo"}}, &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "standard"}})
			c := &Client{core: core}
			i := d.Instance{Software: software, Namespace: "demo"}
			p := d.Parameters{StorageClass: "standard"}
			require.NoError(t, c.CatalogPreflightResources(context.Background(), i, p))
			profile, _ := d.Profile(software)
			sawStorage := false
			for _, action := range core.Actions() {
				require.Equal(t, "get", action.GetVerb())
				require.NotEqual(t, "secrets", action.GetResource().Resource)
				sawStorage = sawStorage || action.GetResource().Resource == "storageclasses"
			}
			require.Equal(t, profile.Persistent, sawStorage)
			p.SecretName, p.SecretKey = "credentials", "password"
			require.ErrorContains(t, c.CatalogPreflightResources(context.Background(), i, p), "SECRET_UNAVAILABLE")
			_, err := core.CoreV1().Secrets("demo").Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: p.SecretName}, Data: map[string][]byte{"wrong": []byte("value")}}, metav1.CreateOptions{})
			require.NoError(t, err)
			require.ErrorContains(t, c.CatalogPreflightResources(context.Background(), i, p), "SECRET_KEY_MISSING")
		})
	}
}

func TestCatalogRejectsUnknownSoftwareBeforeClusterAccess(t *testing.T) {
	core := kubefake.NewSimpleClientset()
	c := &Client{core: core}
	i := d.Instance{Software: "unknown", Namespace: "demo"}
	require.ErrorContains(t, c.CatalogPreflightResources(context.Background(), i, d.Parameters{}), "UNSUPPORTED_SOFTWARE")
	_, err := c.CatalogVerify(context.Background(), d.Gateway{}, d.Deployment{}, i, d.Parameters{})
	require.ErrorContains(t, err, "UNSUPPORTED_SOFTWARE")
	require.Empty(t, core.Actions())
}

func verificationFixture(t *testing.T, address string) (*Client, d.Gateway, d.Deployment) {
	t.Helper()
	host, rawPort, err := net.SplitHostPort(address)
	require.NoError(t, err)
	port, err := strconv.Atoi(rawPort)
	require.NoError(t, err)
	g := d.Gateway{Namespace: "gateway-system", Service: "gateway"}
	core := kubefake.NewSimpleClientset(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: g.Service, Namespace: g.Namespace}, Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: host}}}}})
	return &Client{core: core}, g, d.Deployment{Port: port}
}

func TestCatalogRedisVerificationWithoutAuthentication(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	ping := "*1\r\n$4\r\nPING\r\n"
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		got := make([]byte, len(ping))
		if _, err = io.ReadFull(conn, got); err == nil && string(got) != ping {
			err = fmt.Errorf("expected PING without AUTH")
		}
		if err == nil {
			_, err = io.WriteString(conn, "+PONG\r\n")
		}
		done <- err
	}()
	c, g, dep := verificationFixture(t, listener.Addr().String())
	out, err := c.CatalogVerify(context.Background(), g, dep, d.Instance{Namespace: "demo", Software: "redis"}, d.Parameters{})
	require.NoError(t, err)
	require.NoError(t, <-done)
	require.True(t, out.TCPVerified)
	require.True(t, out.ProtocolVerified)
	for _, action := range c.core.(*kubefake.Clientset).Actions() {
		require.NotEqual(t, "secrets", action.GetResource().Resource)
	}
}

func TestCatalogHTTPAndMilvusVerification(t *testing.T) {
	for _, tc := range []struct {
		name, software, response string
		status                   int
		verified                 bool
	}{
		{"nginx", "nginx", "ok", 200, true},
		{"jupyter-redirect", "sandbox", "", 302, true},
		{"http-auth-failed", "nginx", "", 401, false},
		{"milvus", "milvus", `{"code":0,"data":[]}`, 200, true},
		{"milvus-business-error", "milvus", `{"code":100,"message":"failure"}`, 200, false},
		{"milvus-not-milvus", "milvus", `{}`, 200, false},
		{"milvus-malformed", "milvus", "oops", 200, false},
		{"milvus-server-error", "milvus", `{"code":0,"data":[]}`, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.software == "milvus" {
					require.Equal(t, http.MethodPost, r.Method)
					require.Equal(t, "/v2/vectordb/collections/list", r.URL.Path)
					var body map[string]string
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Equal(t, map[string]string{"dbName": "default"}, body)
				} else {
					require.Equal(t, http.MethodGet, r.Method)
					require.Equal(t, "/", r.URL.Path)
				}
				require.Empty(t, r.Header.Get("Authorization"))
				w.Header().Set("Location", "http://must-not-be-followed.invalid/")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.response)
			}))
			defer server.Close()
			c, g, dep := verificationFixture(t, strings.TrimPrefix(server.URL, "http://"))
			out, err := c.CatalogVerify(context.Background(), g, dep, d.Instance{Software: tc.software}, d.Parameters{})
			require.NoError(t, err)
			require.True(t, out.TCPVerified)
			require.Equal(t, tc.verified, out.ProtocolVerified)
			require.False(t, out.UserNetworkVerified)
		})
	}
}

func TestCatalogDatabaseVerificationRejectsWrongProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadRequest) }))
	defer server.Close()
	for _, software := range []string{"postgres", "mysql", "mongodb"} {
		t.Run(software, func(t *testing.T) {
			c, g, dep := verificationFixture(t, strings.TrimPrefix(server.URL, "http://"))
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			out, err := c.CatalogVerify(ctx, g, dep, d.Instance{Software: software}, d.Parameters{Username: "root", Database: "test"})
			require.NoError(t, err)
			require.True(t, out.TCPVerified)
			require.False(t, out.ProtocolVerified)
			require.Equal(t, "PROTOCOL_CHECK_FAILED", out.Result)
		})
	}
}

func TestCatalogInspectDeploymentRequiresNoPVCAndPreservesIdentity(t *testing.T) {
	c, core, dyn, g := fixture()
	ctx := context.Background()
	c.discovery.(*fake.FakeDiscovery).Resources = append(c.discovery.(*fake.FakeDiscovery).Resources, &metav1.APIResourceList{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{{Name: "deployments", Kind: "Deployment", Namespaced: true}}})
	one := int32(1)
	workload := &appsv1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}, ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "demo", UID: "deployment-uid", Generation: 1, Labels: map[string]string{d.InstanceLabel: "instance"}}, Spec: appsv1.DeploymentSpec{Replicas: &one}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}}
	_, err := core.AppsV1().Deployments("demo").Create(ctx, workload, metav1.CreateOptions{})
	require.NoError(t, err)
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(workload)
	require.NoError(t, err)
	deploymentObject := &unstructured.Unstructured{Object: obj}
	_, err = dyn.Resource(schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}).Namespace("demo").Create(ctx, deploymentObject, metav1.CreateOptions{})
	require.NoError(t, err)
	conditions := []any{map[string]any{"type": "Accepted", "status": "True", "observedGeneration": int64(1)}, map[string]any{"type": "Programmed", "status": "True", "observedGeneration": int64(1)}, map[string]any{"type": "ResolvedRefs", "status": "True", "observedGeneration": int64(1)}}
	route := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1alpha2", "kind": "TCPRoute", "metadata": map[string]any{"name": "web-route", "namespace": "demo", "uid": "route-uid", "generation": int64(1), "labels": map[string]any{d.InstanceLabel: "instance"}}, "status": map[string]any{"parents": []any{map[string]any{"parentRef": map[string]any{"name": g.Name, "namespace": g.Namespace}, "conditions": conditions}}}}}
	_, err = dyn.Resource(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "tcproutes"}).Namespace("demo").Create(ctx, route, metav1.CreateOptions{})
	require.NoError(t, err)
	gatewayResource := dyn.Resource(schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}).Namespace(g.Namespace)
	gw, err := gatewayResource.Get(ctx, g.Name, metav1.GetOptions{})
	require.NoError(t, err)
	gw.SetGeneration(1)
	gw.Object["status"] = map[string]any{"conditions": conditions, "listeners": []any{map[string]any{"name": "olixops-tcp-30032", "conditions": conditions}}}
	_, err = gatewayResource.Update(ctx, gw, metav1.UpdateOptions{})
	require.NoError(t, err)
	svc, err := core.CoreV1().Services(g.Namespace).Get(ctx, g.Service, metav1.GetOptions{})
	require.NoError(t, err)
	svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{Hostname: "gateway.example.test"}}
	_, err = core.CoreV1().Services(g.Namespace).UpdateStatus(ctx, svc, metav1.UpdateOptions{})
	require.NoError(t, err)
	workloadJSON, err := yaml.Marshal(deploymentObject.Object)
	require.NoError(t, err)
	routeJSON, err := yaml.Marshal(route.Object)
	require.NoError(t, err)
	dep := d.Deployment{Port: 30032, YAML: string(workloadJSON) + "\n---\n" + string(routeJSON)}
	out, err := c.CatalogInspect(ctx, dep, g)
	require.NoError(t, err)
	require.True(t, out.WorkloadReady)
	require.True(t, out.PVCBound)
	require.True(t, out.RouteReady)
	require.True(t, out.GatewayReady)
	require.Equal(t, "gateway.example.test:30032", out.Address)
	dep.Resources = []d.Resource{{Kind: "Deployment", Namespace: "demo", Name: "web", UID: "different-uid"}}
	_, err = c.CatalogInspect(ctx, dep, g)
	require.ErrorContains(t, err, "RESOURCE_IDENTITY_CONFLICT")
	dep.Resources = nil
	dep.YAML += "\n---\napiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: missing\n  namespace: demo\n  labels:\n    " + d.InstanceLabel + ": instance\n"
	out, err = c.CatalogInspect(ctx, dep, g)
	require.NoError(t, err)
	require.False(t, out.PVCBound, "a PVC declared by the frozen template must still be required")
	require.Len(t, out.Missing, 1)
}

func TestCatalogVerificationFailsClosedWhenReferencedSecretIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("protocol verification must not run without its referenced credential")
	}))
	defer server.Close()
	c, g, dep := verificationFixture(t, strings.TrimPrefix(server.URL, "http://"))
	i := d.Instance{Namespace: "demo", Software: "redis"}
	p := d.Parameters{SecretName: "credentials", SecretKey: "password"}
	out, err := c.CatalogVerify(context.Background(), g, dep, i, p)
	require.ErrorContains(t, err, "SECRET_UNAVAILABLE")
	require.True(t, out.TCPVerified)
	require.False(t, out.ProtocolVerified)
	_, err = c.core.CoreV1().Secrets(i.Namespace).Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: p.SecretName}, Data: map[string][]byte{"wrong": []byte("never-return-this-value")}}, metav1.CreateOptions{})
	require.NoError(t, err)
	out, err = c.CatalogVerify(context.Background(), g, dep, i, p)
	require.ErrorContains(t, err, "SECRET_KEY_MISSING")
	require.NotContains(t, err.Error(), "never-return-this-value")
	require.False(t, out.ProtocolVerified)
}
