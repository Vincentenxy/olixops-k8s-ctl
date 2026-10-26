package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"olixops-k8s-ctl/internal/application"
	app "olixops-k8s-ctl/internal/application/catalog"
	"olixops-k8s-ctl/internal/domain"
	d "olixops-k8s-ctl/internal/domain/catalog"
	"olixops-k8s-ctl/internal/infrastructure/catalogdb"
	"olixops-k8s-ctl/internal/infrastructure/config"
)

type fakeCluster struct {
	mu            sync.Mutex
	objects       map[string]*unstructured.Unstructured
	external      map[int]d.PortUsage
	snapshotErr   bool
	failApply     bool
	failEnsure    bool
	notReady      bool
	deletePending bool
	writes        int
	seq           int
}

func newFake() *fakeCluster {
	return &fakeCluster{objects: map[string]*unstructured.Unstructured{}, external: map[int]d.PortUsage{30030: {Port: 30030, Exposed: true, Listener: "old-postgres", Refs: []string{"postgres/existing"}, Owners: []string{""}}}}
}
func docs(raw string) []*unstructured.Unstructured {
	dec := kyaml.NewYAMLOrJSONDecoder(strings.NewReader(raw), 4096)
	var out []*unstructured.Unstructured
	for {
		var obj map[string]any
		err := dec.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		if len(obj) > 0 {
			out = append(out, &unstructured.Unstructured{Object: obj})
		}
	}
	return out
}
func key(u *unstructured.Unstructured) string {
	return u.GetKind() + "/" + u.GetNamespace() + "/" + u.GetName()
}
func (f *fakeCluster) Check(ctx context.Context, raw, ns string) error {
	return (application.CatalogCluster{Service: application.NewClusterService(nil, nil, config.KubernetesConfig{})}).Check(ctx, raw, ns)
}
func (f *fakeCluster) Snapshot(context.Context, d.Gateway) (d.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapshotErr {
		return d.Snapshot{}, errors.New("offline")
	}
	snap := d.Snapshot{Ports: map[int]d.PortUsage{}, TCPRouteVersion: "v1alpha2"}
	for p, u := range f.external {
		snap.Ports[p] = u
	}
	for _, u := range f.objects {
		if u.GetKind() != "TCPRoute" {
			continue
		}
		parents, _, _ := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
		p, _, _ := unstructured.NestedInt64(parents[0].(map[string]any), "port")
		v := snap.Ports[int(p)]
		v.Port = int(p)
		v.Refs = append(v.Refs, u.GetNamespace()+"/"+u.GetName())
		v.Owners = append(v.Owners, u.GetLabels()[d.InstanceLabel])
		snap.Ports[int(p)] = v
	}
	return snap, nil
}
func (f *fakeCluster) Preflight(context.Context, d.Instance, d.Parameters) error { return nil }
func (f *fakeCluster) EnsurePort(_ context.Context, _ d.Gateway, p int, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.failEnsure {
		return errors.New("update timeout")
	}
	u := f.external[p]
	u.Port = p
	u.Exposed = true
	u.Listener = name
	f.external[p] = u
	return nil
}
func (f *fakeCluster) Apply(_ context.Context, raw, _ string, dry bool) error {
	if dry {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	for n, obj := range docs(raw) {
		f.seq++
		if old := f.objects[key(obj)]; old != nil {
			obj.SetUID(old.GetUID())
		} else {
			obj.SetUID("uid-" + typesID(f.seq))
		}
		f.objects[key(obj)] = obj
		if f.failApply && n == 1 {
			return errors.New("interrupted after partial apply")
		}
	}
	return nil
}
func typesID(n int) types.UID { return types.UID(fmt.Sprint(n)) }
func (f *fakeCluster) Inventory(_ context.Context, raw string) ([]d.Resource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []d.Resource
	for _, obj := range docs(raw) {
		r := d.Resource{APIVersion: obj.GetAPIVersion(), Kind: obj.GetKind(), Namespace: obj.GetNamespace(), Name: obj.GetName()}
		if live := f.objects[key(obj)]; live != nil {
			if live.GetLabels()[d.InstanceLabel] != obj.GetLabels()[d.InstanceLabel] {
				return nil, d.Fail("RESOURCE_OWNERSHIP_CONFLICT", "foreign resource")
			}
			r.UID = string(live.GetUID())
		}
		out = append(out, r)
	}
	return out, nil
}
func (f *fakeCluster) Inspect(ctx context.Context, dep d.Deployment, _ d.Gateway) (d.Observation, error) {
	inv, err := f.Inventory(ctx, dep.YAML)
	o := d.Observation{Resources: inv, WorkloadReady: !f.notReady, PVCBound: !f.notReady, GatewayReady: !f.notReady, RouteReady: !f.notReady}
	for _, r := range inv {
		if r.UID == "" {
			o.Missing = append(o.Missing, r)
		}
	}
	return o, err
}
func (f *fakeCluster) Delete(_ context.Context, r d.Resource) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	k := r.Kind + "/" + r.Namespace + "/" + r.Name
	if old := f.objects[k]; old != nil {
		if string(old.GetUID()) != r.UID {
			return errors.New("UID conflict")
		}
		if !f.deletePending {
			delete(f.objects, k)
		}
	}
	return nil
}
func gateway() d.Gateway {
	return d.Gateway{ID: "higress", Cluster: "configured", Namespace: "higress-system", Name: "higress-tcp-gateway", Service: "higress-gateway", PortStart: 30000, PortEnd: 30200}
}
func setup(t *testing.T) (*app.Service, *catalogdb.Store, *fakeCluster, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.db")
	store, err := catalogdb.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	f := newFake()
	s, err := app.New(context.Background(), store, f, gateway(), nil)
	require.NoError(t, err)
	return s, store, f, path
}
func input(owner, sw, req string) app.SaveConfigInput {
	return app.SaveConfigInput{OwnerID: owner, Name: sw + "-demo", Namespace: "demo", Software: sw, TemplateVersion: 1, Parameters: d.Parameters{StorageClass: "standard", SecretName: owner + "-" + sw}, RequestID: req}
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *d.Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, want, e.Code)
}

func TestCatalogPersistenceRevisionsAndTemplates(t *testing.T) {
	ctx := context.Background()
	s, store, f, path := setup(t)
	a, err := s.SaveConfig(ctx, input("alice", "redis", "a"))
	require.NoError(t, err)
	b, err := s.SaveConfig(ctx, input("bob", "postgres", "b"))
	require.NoError(t, err)
	require.NotEqual(t, a.Instance.ResourceName, b.Instance.ResourceName)
	require.Contains(t, a.Revision.YAML, "$(REDIS_PASSWORD)")
	require.NotContains(t, a.Revision.YAML, "kind: Secret")
	retry, err := s.SaveConfig(ctx, input("alice", "redis", "a"))
	require.NoError(t, err)
	require.Equal(t, a, retry)
	changed := input("bob", "redis", "a")
	_, err = s.SaveConfig(ctx, changed)
	code(t, err, "IDEMPOTENCY_CONFLICT")
	update := input("alice", "redis", "a2")
	update.InstanceID = a.Instance.ID
	update.ExpectedRevision = 1
	update.Parameters.Storage = "2Gi"
	saved, err := s.SaveConfig(ctx, update)
	require.NoError(t, err)
	require.Equal(t, 2, saved.Revision.Number)
	update.RequestID = "stale"
	_, err = s.SaveConfig(ctx, update)
	code(t, err, "REVISION_CONFLICT")
	update.ExpectedRevision = 2
	update.Parameters.Storage = "512Mi"
	_, err = s.SaveConfig(ctx, update)
	code(t, err, "STORAGE_SHRINK_FORBIDDEN")
	missing := input("alice", "redis", "missing")
	missing.Parameters.SecretName = ""
	_, err = s.SaveConfig(ctx, missing)
	code(t, err, "MISSING_PARAMETER")
	preview, err := s.PreviewConfig(ctx, input("alice", "postgres", ""))
	require.NoError(t, err)
	require.NotEmpty(t, preview.Revision.YAML)
	old, err := s.Template(ctx, app.TemplateInput{Software: "redis", Version: 1})
	require.NoError(t, err)
	next, err := s.SaveTemplate(ctx, app.SaveTemplateInput{Software: "redis", Version: 2, Defaults: old.Defaults})
	require.NoError(t, err)
	require.Equal(t, 2, next.Version)
	_, err = s.SaveTemplate(ctx, app.SaveTemplateInput{Software: "redis", Version: 1, Defaults: d.Parameters{Image: "redis:7.4.3"}})
	code(t, err, "TEMPLATE_VERSION_EXISTS")
	require.Zero(t, f.writes)
	require.NoError(t, store.Close())
	reopened, err := catalogdb.Open(path)
	require.NoError(t, err)
	defer reopened.Close()
	s2, err := app.New(ctx, reopened, f, gateway(), nil)
	require.NoError(t, err)
	rs, err := s2.Revisions(ctx, app.ListInput{InstanceID: a.Instance.ID})
	require.NoError(t, err)
	require.Len(t, rs, 2)
	require.Equal(t, a.Revision, rs[0])
	ports, err := s2.Ports(ctx, app.PortInput{Limit: 500})
	require.NoError(t, err)
	require.Len(t, ports.Items, 201)
	require.Equal(t, "unchecked", ports.Items[0].State)
}
func TestPortConcurrencyImportAndRollback(t *testing.T) {
	ctx := context.Background()
	s, store, f, path := setup(t)
	a, err := s.SaveConfig(ctx, input("alice", "redis", "a"))
	require.NoError(t, err)
	b, err := s.SaveConfig(ctx, input("bob", "redis", "b"))
	require.NoError(t, err)
	ports, err := s.SyncPorts(ctx)
	require.NoError(t, err)
	require.Len(t, ports.Items, 201)
	_, err = s.ReservePort(ctx, app.ReserveInput{InstanceID: a.Instance.ID, Port: 20030, RequestID: "invalid"})
	code(t, err, "PORT_NOT_IN_POOL")
	_, err = s.ReservePort(ctx, app.ReserveInput{InstanceID: a.Instance.ID, Port: 30030, RequestID: "occupied"})
	code(t, err, "PORT_IN_USE")
	// Separate DB handles and coordinators prove SQL serialization, not just a Go mutex.
	other, err := catalogdb.Open(path)
	require.NoError(t, err)
	defer other.Close()
	s2, err := app.New(ctx, other, f, gateway(), nil)
	require.NoError(t, err)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for n, entry := range []struct {
		s  *app.Service
		id string
	}{{s, a.Instance.ID}, {s2, b.Instance.ID}} {
		wg.Add(1)
		go func(n int, e struct {
			s  *app.Service
			id string
		}) {
			defer wg.Done()
			_, err := e.s.ReservePort(ctx, app.ReserveInput{InstanceID: e.id, Port: 30031, RequestID: fmt.Sprint("race", n)})
			errs <- err
		}(n, entry)
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		} else {
			code(t, e, "PORT_IN_USE")
		}
	}
	require.Equal(t, 1, success)
	err = store.Update(ctx, func(st *d.State) error {
		p := st.Ports["higress:TCP:30032"]
		p.State = "reserved"
		st.Ports[p.ID] = p
		return errors.New("abort")
	})
	require.Error(t, err)
	require.NoError(t, store.View(ctx, func(st *d.State) error { require.Equal(t, "free", st.Ports["higress:TCP:30032"].State); return nil }))
	f.snapshotErr = true
	_, err = s.SyncPorts(ctx)
	code(t, err, "GATEWAY_SYNC_INCOMPLETE")
	p, err := s.Ports(ctx, app.PortInput{Port: 30032})
	require.NoError(t, err)
	require.Equal(t, "unchecked", p.Items[0].State)
}
func TestDeploymentPartialFailureRecoveryAndDeletion(t *testing.T) {
	ctx := context.Background()
	s, store, f, path := setup(t)
	cfg, err := s.SaveConfig(ctx, input("alice", "redis", "cfg"))
	require.NoError(t, err)
	prep := app.PrepareInput{InstanceID: cfg.Instance.ID, Revision: 1, Port: 30032, RequestID: "prepare"}
	dep, err := s.Prepare(ctx, prep)
	require.NoError(t, err)
	require.Contains(t, dep.YAML, "port: 30032")
	require.Contains(t, dep.YAML, "port: 6379")
	again, err := s.Prepare(ctx, prep)
	require.NoError(t, err)
	require.Equal(t, dep.ID, again.ID)
	prep.Port = 30033
	_, err = s.Prepare(ctx, prep)
	code(t, err, "IDEMPOTENCY_CONFLICT")
	f.failApply = true
	unknown, err := s.Deploy(ctx, app.DeploymentInput{DeploymentID: dep.ID})
	code(t, err, "DEPLOYMENT_RESULT_UNKNOWN")
	require.Equal(t, "unknown", unknown.Phase)
	require.NotEmpty(t, unknown.Resources[0].UID)
	_, err = s.ReleasePort(ctx, app.ReleaseInput{Port: 30032, RequestID: "release"})
	code(t, err, "DEPLOYMENT_ACTIVE")
	// Persisted in-flight state is reconciled to unknown on startup, never freed.
	require.NoError(t, store.Update(ctx, func(st *d.State) error {
		v := st.Deployments[dep.ID]
		v.Phase = "applying"
		st.Deployments[dep.ID] = v
		return nil
	}))
	other, err := catalogdb.Open(path)
	require.NoError(t, err)
	defer other.Close()
	s2, err := app.New(ctx, other, f, gateway(), nil)
	require.NoError(t, err)
	history, err := s2.Deployments(ctx, app.ListInput{InstanceID: cfg.Instance.ID})
	require.NoError(t, err)
	require.Equal(t, "unknown", history[0].Phase)
	f.failApply = false
	f.notReady = true
	deployed, err := s2.Deploy(ctx, app.DeploymentInput{DeploymentID: dep.ID})
	require.NoError(t, err)
	require.Equal(t, "applied", deployed.Phase)
	require.False(t, deployed.Observation.TCPVerified)
	writes := f.writes
	_, err = s2.Deploy(ctx, app.DeploymentInput{DeploymentID: dep.ID})
	require.NoError(t, err)
	require.Equal(t, writes, f.writes)
	f.deletePending = true
	_, err = s2.DeleteInstance(ctx, app.DeleteInput{InstanceID: cfg.Instance.ID, RequestID: "delete"})
	code(t, err, "DELETE_PENDING")
	p, err := s2.Ports(ctx, app.PortInput{Port: 30032})
	require.NoError(t, err)
	require.Equal(t, "releasing", p.Items[0].State)
	f.deletePending = false
	deleted, err := s2.DeleteInstance(ctx, app.DeleteInput{InstanceID: cfg.Instance.ID, RequestID: "delete"})
	require.NoError(t, err)
	require.Equal(t, "deleted", deleted.Phase)
	require.Len(t, f.objects, 1)
	for _, v := range f.objects {
		require.Equal(t, "PersistentVolumeClaim", v.GetKind())
	}
	p, err = s2.Ports(ctx, app.PortInput{Port: 30032})
	require.NoError(t, err)
	require.Equal(t, "free", p.Items[0].State)
	require.True(t, p.Items[0].Exposed)
	require.Contains(t, f.external[30030].Refs, "postgres/existing")
}
func TestDeletionRejectsReplacedUIDAndForeignRoute(t *testing.T) {
	ctx := context.Background()
	s, _, f, _ := setup(t)
	cfg, err := s.SaveConfig(ctx, input("alice", "redis", "cfg"))
	require.NoError(t, err)
	dep, err := s.Prepare(ctx, app.PrepareInput{InstanceID: cfg.Instance.ID, Revision: 1, Port: 30034, RequestID: "p"})
	require.NoError(t, err)
	dep, err = s.Deploy(ctx, app.DeploymentInput{DeploymentID: dep.ID})
	require.NoError(t, err)
	for _, obj := range f.objects {
		if obj.GetKind() == "StatefulSet" {
			obj.SetUID("replaced")
		}
	}
	before := f.writes
	_, err = s.DeleteInstance(ctx, app.DeleteInput{InstanceID: cfg.Instance.ID, RequestID: "delete"})
	code(t, err, "RESOURCE_IDENTITY_CONFLICT")
	require.Equal(t, before, f.writes)
}

func (f *fakeCluster) Verify(context.Context, d.Gateway, d.Deployment, d.Instance, d.Parameters) (d.AccessReport, error) {
	return d.AccessReport{}, nil
}

func TestFourIndependentInstancesAndPreparedCancellation(t *testing.T) {
	ctx := context.Background()
	s, _, f, _ := setup(t)
	var ids []string
	for n, e := range []struct{ owner, software string }{{"alice", "redis"}, {"alice", "postgres"}, {"bob", "redis"}, {"bob", "postgres"}} {
		cfg, err := s.SaveConfig(ctx, input(e.owner, e.software, fmt.Sprint("cfg-", n)))
		require.NoError(t, err)
		ids = append(ids, cfg.Instance.ID)
		dep, err := s.Prepare(ctx, app.PrepareInput{InstanceID: cfg.Instance.ID, Revision: 1, Port: 30040 + n, RequestID: fmt.Sprint("prep-", n)})
		require.NoError(t, err)
		dep, err = s.Deploy(ctx, app.DeploymentInput{DeploymentID: dep.ID})
		require.NoError(t, err)
		require.Equal(t, "ready", dep.Phase)
	}
	require.Len(t, f.objects, 20)
	pvcNames := map[string]bool{}
	for _, obj := range f.objects {
		if obj.GetKind() == "PersistentVolumeClaim" {
			pvcNames[obj.GetName()] = true
		}
	}
	require.Len(t, pvcNames, 4)
	// Deleting an instance releases its port even though historical deployments remain.
	_, err := s.DeleteInstance(ctx, app.DeleteInput{InstanceID: ids[0], RequestID: "delete-first"})
	require.NoError(t, err)
	cfg, err := s.SaveConfig(ctx, input("carol", "redis", "carol"))
	require.NoError(t, err)
	in := app.PrepareInput{InstanceID: cfg.Instance.ID, Revision: 1, Port: 30040, RequestID: "carol-prep"}
	dep, err := s.Prepare(ctx, in)
	require.NoError(t, err)
	_, err = s.ReleasePort(ctx, app.ReleaseInput{Port: 30040, RequestID: "cancel-carol"})
	require.NoError(t, err)
	in.RequestID = "carol-prepare-again"
	next, err := s.Prepare(ctx, in)
	require.NoError(t, err)
	require.NotEqual(t, dep.ID, next.ID)
}
func TestForeignRouteAppearingAfterPrepareBlocksDeploy(t *testing.T) {
	ctx := context.Background()
	s, _, f, _ := setup(t)
	cfg, err := s.SaveConfig(ctx, input("alice", "redis", "cfg"))
	require.NoError(t, err)
	dep, err := s.Prepare(ctx, app.PrepareInput{InstanceID: cfg.Instance.ID, Revision: 1, Port: 30042, RequestID: "prepare"})
	require.NoError(t, err)
	f.external[30042] = d.PortUsage{Port: 30042, Refs: []string{"foreign/new-route"}, Owners: []string{""}}
	_, err = s.Deploy(ctx, app.DeploymentInput{DeploymentID: dep.ID})
	code(t, err, "PORT_IN_USE")
	require.Zero(t, f.writes)
	_, err = s.ReleasePort(ctx, app.ReleaseInput{Port: 30042, RequestID: "release"})
	code(t, err, "PORT_IN_USE")
}

type rejectPolicy struct{}

func (rejectPolicy) Authorize(context.Context, domain.AuthorizationRequest) error {
	return errors.New("policy denied")
}
func TestCatalogPolicySeamRunsBeforeDBAndClusterSideEffects(t *testing.T) {
	ctx := context.Background()
	_, store, f, _ := setup(t)
	s, err := app.New(ctx, store, f, gateway(), rejectPolicy{})
	require.NoError(t, err)
	_, err = s.SaveConfig(ctx, input("alice", "redis", "deny"))
	require.EqualError(t, err, "policy denied")
	_, err = s.SyncPorts(ctx)
	require.EqualError(t, err, "policy denied")
	require.NoError(t, store.View(ctx, func(st *d.State) error {
		require.Empty(t, st.Instances)
		for _, p := range st.Ports {
			require.Equal(t, "unchecked", p.State)
		}
		return nil
	}))
	require.Zero(t, f.writes)
}

func TestManualReservationSinglePortAndConfigOnlyDeletion(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := setup(t)
	cfg, err := s.SaveConfig(ctx, input("alice", "redis", "cfg"))
	require.NoError(t, err)
	_, err = s.ReservePort(ctx, app.ReserveInput{InstanceID: cfg.Instance.ID, Port: 30044, RequestID: "first"})
	require.NoError(t, err)
	_, err = s.ReservePort(ctx, app.ReserveInput{InstanceID: cfg.Instance.ID, Port: 30045, RequestID: "second"})
	code(t, err, "INSTANCE_PORT_ALREADY_RESERVED")
	_, err = s.DeleteInstance(ctx, app.DeleteInput{InstanceID: cfg.Instance.ID, RequestID: "delete"})
	require.NoError(t, err)
	port, err := s.Ports(ctx, app.PortInput{Port: 30044})
	require.NoError(t, err)
	require.Equal(t, "free", port.Items[0].State)
}
