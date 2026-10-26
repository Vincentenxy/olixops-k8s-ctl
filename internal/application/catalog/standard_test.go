package catalog_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	app "olixops-k8s-ctl/internal/application/catalog"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

func standardSetup(t *testing.T) (*app.Service, d.Repository, *fakeCluster) {
	_, store, f, _ := setup(t)
	s, err := app.New(context.Background(), store, f, gateway(), nil, app.WithDefaults(app.Defaults{OwnerID: "poc", StorageClass: "standard"}))
	require.NoError(t, err)
	return s, store, f
}
func TestStandardSevenDefaultsAndIdempotency(t *testing.T) {
	ctx := context.Background()
	s, store, f := standardSetup(t)
	listed, err := s.StandardServices(ctx)
	require.NoError(t, err)
	require.Len(t, listed.Items, 7)
	for _, sw := range []string{"redis", "pg", "mysql", "mongo", "milvus", "nginx", "沙箱"} {
		in := app.DeployStandardInput{Namespace: "demo", Software: sw, RequestID: "quick-" + sw}
		a, err := s.DeployStandard(ctx, in)
		require.NoError(t, err, sw)
		require.NotEmpty(t, a.InternalAddress)
		require.GreaterOrEqual(t, a.Deployment.Port, 30000)
		require.Equal(t, "ready", a.Deployment.Phase)
		writes := f.writes
		// A retry after an environment/default change must use the frozen revision.
		restarted, err := app.New(ctx, store, f, gateway(), nil, app.WithDefaults(app.Defaults{OwnerID: "other", StorageClass: "changed"}))
		require.NoError(t, err)
		b, err := restarted.DeployStandard(ctx, in)
		require.NoError(t, err)
		require.Equal(t, a.Instance.ID, b.Instance.ID)
		require.Equal(t, a.Deployment.ID, b.Deployment.ID)
		require.Equal(t, writes, f.writes)
		in.Parameters.Memory = "2Gi"
		_, err = s.DeployStandard(ctx, in)
		code(t, err, "IDEMPOTENCY_CONFLICT")
	}
	require.NoError(t, store.View(ctx, func(st *d.State) error {
		require.Len(t, st.Instances, 7)
		require.Len(t, st.Revisions, 7)
		require.Len(t, st.Deployments, 7)
		return nil
	}))
}
func TestStandardPartialFailureResumeAndConcurrentAllocation(t *testing.T) {
	ctx := context.Background()
	s, store, f := standardSetup(t)
	in := app.DeployStandardInput{Namespace: "demo", Software: "nginx", RequestID: "partial"}
	f.failApply = true
	a, err := s.DeployStandard(ctx, in)
	code(t, err, "DEPLOYMENT_RESULT_UNKNOWN")
	require.NotEmpty(t, a.Deployment.ID)
	f.failApply = false
	b, err := s.DeployStandard(ctx, in)
	require.NoError(t, err)
	require.Equal(t, a.Deployment.ID, b.Deployment.ID)
	var wg sync.WaitGroup
	results := make(chan app.StandardDeployment, 6)
	errs := make(chan error, 6)
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v, e := s.DeployStandard(ctx, app.DeployStandardInput{Namespace: "demo", Software: "redis", RequestID: fmt.Sprint("parallel-", n)})
			results <- v
			errs <- e
		}(n)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	ports := map[int]bool{}
	for r := range results {
		require.False(t, ports[r.Deployment.Port])
		ports[r.Deployment.Port] = true
	}
	require.NoError(t, store.View(ctx, func(st *d.State) error { require.Len(t, st.Instances, 7); return nil }))
}
func TestSandboxExpiryPersistsAndReclaimsThroughDelete(t *testing.T) {
	ctx := context.Background()
	s, store, f := standardSetup(t)
	in := app.DeployStandardInput{Namespace: "demo", Software: "sandbox", RequestID: "lease", Parameters: d.Parameters{TTLSeconds: 60}}
	a, err := s.DeployStandard(ctx, in)
	require.NoError(t, err)
	require.NotNil(t, a.Deployment.ExpiresAt)
	before := a.Deployment.ExpiresAt.Add(-time.Second)
	require.NoError(t, s.ExpireSandboxes(ctx, before))
	i, err := s.Instance(ctx, app.InstanceInput{InstanceID: a.Instance.ID})
	require.NoError(t, err)
	require.False(t, i.Deleted)
	restarted, err := app.New(ctx, store, f, gateway(), nil)
	require.NoError(t, err)
	f.deletePending = true
	require.Error(t, restarted.ExpireSandboxes(ctx, a.Deployment.ExpiresAt.Add(time.Second)))
	i, err = s.Instance(ctx, app.InstanceInput{InstanceID: a.Instance.ID})
	require.NoError(t, err)
	require.False(t, i.Deleted)
	f.deletePending = false
	require.NoError(t, restarted.ExpireSandboxes(ctx, a.Deployment.ExpiresAt.Add(time.Second)))
	i, err = s.Instance(ctx, app.InstanceInput{InstanceID: a.Instance.ID})
	require.NoError(t, err)
	require.True(t, i.Deleted)
	_, err = s.DeployStandard(ctx, in)
	require.Error(t, err)
	require.NoError(t, store.View(ctx, func(st *d.State) error {
		require.Equal(t, "free", st.Ports[fmt.Sprintf("%s:TCP:%d", gateway().ID, a.Deployment.Port)].State)
		return nil
	}))
}
func TestStandardInvalidBeforeSaving(t *testing.T) {
	ctx := context.Background()
	s, store, _ := standardSetup(t)
	for _, in := range []app.DeployStandardInput{{Namespace: "demo", Software: "unknown", RequestID: "invalid"}, {Namespace: "demo", Software: "redis"}, {Namespace: "demo", Software: "sandbox", RequestID: "ttl", Parameters: d.Parameters{TTLSeconds: -1}}} {
		_, err := s.DeployStandard(ctx, in)
		require.Error(t, err)
	}
	require.NoError(t, store.View(ctx, func(st *d.State) error { require.Empty(t, st.Instances); return nil }))
}

func TestStandardConcurrentSameRequest(t *testing.T) {
	ctx := context.Background()
	s, store, _ := standardSetup(t)
	var wg sync.WaitGroup
	ids := make(chan string, 5)
	errs := make(chan error, 5)
	for n := 0; n < 5; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.DeployStandard(ctx, app.DeployStandardInput{Namespace: "demo", Software: "nginx", RequestID: "same"})
			ids <- v.Instance.ID
			errs <- e
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		require.Equal(t, first, id)
	}
	require.NoError(t, store.View(ctx, func(st *d.State) error {
		require.Len(t, st.Instances, 1)
		require.Len(t, st.Deployments, 1)
		require.Len(t, st.Revisions, 1)
		return nil
	}))
}

type namespaceCluster struct {
	*fakeCluster
	ensured      int
	creates      int
	exists       bool
	ensureErr    error
	preflightErr error
}

func (f *namespaceCluster) ensureNamespace(_ context.Context, ns string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured++
	if f.ensureErr != nil {
		return f.ensureErr
	}
	if !f.exists {
		f.creates++
		f.exists = true
	}
	return nil
}
func (f *namespaceCluster) Preflight(ctx context.Context, i d.Instance, _ d.Parameters) error {
	if err := f.ensureNamespace(ctx, i.Namespace); err != nil {
		return err
	}
	return f.preflightErr
}
func TestStandardExplicitNamespaceBeforeSideEffects(t *testing.T) {
	ctx := context.Background()
	_, store, base, _ := setup(t)
	f := &namespaceCluster{fakeCluster: base}
	s, err := app.New(ctx, store, f, gateway(), nil, app.WithDefaults(app.Defaults{OwnerID: "poc", StorageClass: "standard"}))
	require.NoError(t, err)
	for _, ns := range []string{"", "  ", "Invalid", "other/ns"} {
		_, err = s.DeployStandard(ctx, app.DeployStandardInput{Software: "pg", RequestID: "bad-ns", Namespace: ns})
		if ns == "" || ns == "  " {
			code(t, err, "MISSING_NAMESPACE")
		} else {
			code(t, err, "INVALID_NAMESPACE")
		}
	}
	require.Zero(t, f.ensured)
	require.Zero(t, f.writes)
	require.NoError(t, store.View(ctx, func(st *d.State) error {
		require.Empty(t, st.Instances)
		require.Empty(t, st.Revisions)
		require.Empty(t, st.Deployments)
		require.Empty(t, st.Events)
		return nil
	}))
}
func TestStandardNamespaceFailureResumeAndRetention(t *testing.T) {
	ctx := context.Background()
	_, store, base, _ := setup(t)
	f := &namespaceCluster{fakeCluster: base, ensureErr: d.Fail("NAMESPACE_CREATION_FAILED", "denied")}
	s, err := app.New(ctx, store, f, gateway(), nil, app.WithDefaults(app.Defaults{OwnerID: "poc", StorageClass: "standard"}))
	require.NoError(t, err)
	in := app.DeployStandardInput{Software: "pg", RequestID: "namespace-resume", Namespace: "demo"}
	a, err := s.DeployStandard(ctx, in)
	code(t, err, "NAMESPACE_CREATION_FAILED")
	require.Equal(t, "prepared", a.Deployment.Phase)
	require.Zero(t, f.writes)
	f.ensureErr = nil
	b, err := s.DeployStandard(ctx, in)
	require.NoError(t, err)
	require.Equal(t, a.Deployment.ID, b.Deployment.ID)
	require.Equal(t, 1, f.creates)
	_, err = s.DeployStandard(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 1, f.creates)
	_, err = s.DeleteInstance(ctx, app.DeleteInput{InstanceID: b.Instance.ID, RequestID: "delete-ns-service"})
	require.NoError(t, err)
	require.True(t, f.exists, "namespace is shared and must survive instance deletion")
	require.NoError(t, store.View(ctx, func(st *d.State) error {
		require.Len(t, st.Instances, 1)
		require.Len(t, st.Revisions, 1)
		require.Len(t, st.Deployments, 1)
		return nil
	}))
}

func TestStandardCreatedNamespaceSurvivesLaterFailure(t *testing.T) {
	ctx := context.Background()
	_, store, base, _ := setup(t)
	f := &namespaceCluster{fakeCluster: base, preflightErr: d.Fail("STORAGECLASS_UNAVAILABLE", "missing storage")}
	s, err := app.New(ctx, store, f, gateway(), nil, app.WithDefaults(app.Defaults{OwnerID: "poc", StorageClass: "standard"}))
	require.NoError(t, err)
	in := app.DeployStandardInput{Software: "pg", Namespace: "demo", RequestID: "ns-before-storage"}
	a, err := s.DeployStandard(ctx, in)
	code(t, err, "STORAGECLASS_UNAVAILABLE")
	require.True(t, f.exists)
	require.Equal(t, 1, f.creates)
	require.Zero(t, f.writes)
	f.preflightErr = nil
	b, err := s.DeployStandard(ctx, in)
	require.NoError(t, err)
	require.Equal(t, a.Deployment.ID, b.Deployment.ID)
	require.Equal(t, 1, f.creates)
}
