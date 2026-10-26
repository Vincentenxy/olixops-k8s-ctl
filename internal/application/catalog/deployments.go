package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

type PrepareInput struct {
	InstanceID string `json:"instanceId"`
	Revision   int    `json:"revision"`
	Port       int    `json:"port"`
	RequestID  string `json:"requestId"`
}
type DeleteInput struct {
	InstanceID string `json:"instanceId"`
	RequestID  string `json:"requestId"`
}

func routeYAML(i d.Instance, g d.Gateway, port, revision int, version string) (string, error) {
	raw := fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/%s
kind: TCPRoute
metadata:
  name: %s-route
  namespace: %s
spec:
  parentRefs:
    - name: %s
      namespace: %s
      sectionName: %s
      port: %d
  rules:
    - backendRefs:
        - name: %s
          port: %d
`, version, i.ResourceName, i.Namespace, g.Name, g.Namespace, listenerName(port), port, i.ResourceName, backend(i.Software))
	return stamp(raw, i.ID, revision)
}
func (s *Service) Prepare(ctx context.Context, in PrepareInput) (d.Deployment, error) {
	var out d.Deployment
	if err := s.policy(ctx, "deployment.prepare", true, in); err != nil {
		return out, err
	}
	if !validRequest(in.RequestID) {
		return out, d.Fail("MISSING_PARAMETER", "requestId is required")
	}
	unlock := s.lock(in.InstanceID)
	defer unlock()
	s.gatewayLock.Lock()
	defer s.gatewayLock.Unlock()
	found := false
	err := s.repo.View(ctx, func(st *d.State) error {
		for _, dep := range st.Deployments {
			if dep.RequestID == in.RequestID {
				if dep.RequestDigest != digest(in) {
					return d.Fail("IDEMPOTENCY_CONFLICT", "requestId has different input")
				}
				out = dep
				found = true
			}
		}
		return nil
	})
	if err != nil || found {
		return out, err
	}
	snap, err := s.sync(ctx)
	if err != nil {
		return out, err
	}
	err = s.repo.Update(ctx, func(st *d.State) error {
		i, ok := st.Instances[in.InstanceID]
		if !ok || i.Deleted {
			return d.Fail("NOT_FOUND", "active instance does not exist")
		}
		r, ok := st.Revisions[fmt.Sprintf("%s:%d", in.InstanceID, in.Revision)]
		if !ok {
			return d.Fail("NOT_FOUND", "configuration revision does not exist")
		}
		port := in.Port
		if i.DeploymentID != "" {
			old := st.Deployments[i.DeploymentID]
			if old.Phase != "applied" && old.Phase != "ready" {
				return d.Fail("DEPLOYMENT_ACTIVE", "reconcile, deploy or delete the existing deployment first")
			}
			if port == 0 {
				port = old.Port
			}
			if old.Port != port {
				return d.Fail("PORT_CHANGE_REQUIRES_DELETE", "delete existing instance deployment before changing external ports")
			}
		}
		if port == 0 {
			for _, existing := range st.Ports {
				if existing.GatewayID == s.gateway.ID && existing.InstanceID == i.ID && existing.State == "reserved" && !existing.Conflict {
					port = existing.Port
					break
				}
			}
		}
		if port == 0 {
			for candidate := s.gateway.PortStart; candidate <= s.gateway.PortEnd; candidate++ {
				p := st.Ports[s.portID(candidate)]
				if (p.State == "free" && !p.Conflict && len(p.ExternalRefs) == 0) || (p.InstanceID == i.ID && p.State == "reserved" && !p.Conflict) {
					port = candidate
					break
				}
			}
			if port == 0 {
				return d.Fail("PORT_POOL_EXHAUSTED", "no available external port")
			}
		}
		p, err := s.claim(st, i, port, in.RequestID)
		if err != nil {
			return err
		}
		route, err := routeYAML(i, s.gateway, port, in.Revision, snap.TCPRouteVersion)
		if err != nil {
			return err
		}
		raw := r.YAML + "---\n" + route
		if err = s.cluster.Check(ctx, raw, i.Namespace); err != nil {
			return err
		}
		now := time.Now().UTC()
		out = d.Deployment{ID: uuid.NewString(), InstanceID: i.ID, Revision: r.Number, Port: port, GatewayID: s.gateway.ID, YAML: raw, Digest: digest(raw), RequestID: in.RequestID, RequestDigest: digest(in), Phase: "prepared", Stage: "prepared", CreatedAt: now, UpdatedAt: now}
		// Preserve known UIDs across configuration revisions and crash recovery.
		if i.DeploymentID != "" {
			out.Resources = st.Deployments[i.DeploymentID].Resources
			out.ExpiresAt = st.Deployments[i.DeploymentID].ExpiresAt
		}
		p.DeploymentID = out.ID
		p.Route = i.ResourceName + "-route"
		p.Listener = listenerName(port)
		st.Ports[p.ID] = p
		i.DeploymentID = out.ID
		st.Instances[i.ID] = i
		st.Deployments[out.ID] = out
		s.event(st, p, "bind", in.RequestID, digest(in))
		return nil
	})
	return out, err
}
func (s *Service) getDeployment(ctx context.Context, id string) (d.Deployment, error) {
	var out d.Deployment
	err := s.repo.View(ctx, func(st *d.State) error {
		var ok bool
		out, ok = st.Deployments[id]
		if !ok {
			return d.Fail("NOT_FOUND", "deployment does not exist")
		}
		if out.GatewayID != s.gateway.ID {
			return d.Fail("GATEWAY_NOT_CONFIGURED", "deployment belongs to another gateway; restore its configured gateway")
		}
		return nil
	})
	return out, err
}
func (s *Service) Deployments(ctx context.Context, in ListInput) ([]d.Deployment, error) {
	out := []d.Deployment{}
	if err := s.policy(ctx, "deployment.list", false, in); err != nil {
		return nil, err
	}
	err := s.repo.View(ctx, func(st *d.State) error {
		for _, v := range st.Deployments {
			if in.InstanceID == "" || v.InstanceID == in.InstanceID {
				out = append(out, v)
			}
		}
		return nil
	})
	sortBy(out, func(v d.Deployment) string { return v.CreatedAt.Format(time.RFC3339Nano) + v.ID })
	return page(out, in), err
}

// Deployment reads the live cluster without changing the database. Saved phase
// and live readiness are intentionally distinct; a read tool is genuinely read-only.
func (s *Service) Deployment(ctx context.Context, in DeploymentInput) (d.Deployment, error) {
	if err := s.policy(ctx, "deployment.get", false, in); err != nil {
		return d.Deployment{}, err
	}
	dep, err := s.getDeployment(ctx, in.DeploymentID)
	if err != nil {
		return dep, err
	}
	dep.Observation, err = s.cluster.Inspect(ctx, dep, s.gateway)
	return dep, err
}
func (s *Service) persist(ctx context.Context, dep d.Deployment) error {
	// Save an interrupted operation even after the client's request timed out.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	dep.UpdatedAt = time.Now().UTC()
	return s.repo.Update(cleanup, func(st *d.State) error { st.Deployments[dep.ID] = dep; return nil })
}
func mergeInventory(pinned, current []d.Resource) ([]d.Resource, error) {
	for _, old := range pinned {
		if old.UID == "" {
			continue
		}
		for _, cur := range current {
			if old.Kind == cur.Kind && old.Namespace == cur.Namespace && old.Name == cur.Name && cur.UID != "" && cur.UID != old.UID {
				return nil, d.Fail("RESOURCE_IDENTITY_CONFLICT", "a recorded resource was replaced; manual reconciliation is required")
			}
		}
	}
	// Keep missing objects' recorded UIDs too, so recreation cannot erase history.
	for n, cur := range current {
		if cur.UID != "" {
			continue
		}
		for _, old := range pinned {
			if old.Kind == cur.Kind && old.Namespace == cur.Namespace && old.Name == cur.Name {
				current[n].UID = old.UID
			}
		}
	}
	return current, nil
}
func (s *Service) Deploy(ctx context.Context, in DeploymentInput) (d.Deployment, error) {
	if err := s.policy(ctx, "deployment.execute", true, in); err != nil {
		return d.Deployment{}, err
	}
	dep, err := s.getDeployment(ctx, in.DeploymentID)
	if err != nil {
		return dep, err
	}
	unlock := s.lock(dep.InstanceID)
	defer unlock()
	s.gatewayLock.Lock()
	defer s.gatewayLock.Unlock()
	dep, err = s.getDeployment(ctx, in.DeploymentID)
	if err != nil {
		return dep, err
	}
	if dep.Phase == "deleted" || dep.Phase == "cancelled" || dep.Stage == "deleting" || dep.Stage == "deleted" {
		return dep, d.Fail("DEPLOYMENT_INACTIVE", "deployment is cancelled or being deleted")
	}
	var i d.Instance
	var rev d.Revision
	err = s.repo.View(ctx, func(st *d.State) error {
		i = st.Instances[dep.InstanceID]
		rev = st.Revisions[fmt.Sprintf("%s:%d", i.ID, dep.Revision)]
		if i.DeploymentID != dep.ID || i.Deleted {
			return d.Fail("DEPLOYMENT_SUPERSEDED", "only the current deployment can execute")
		}
		p := st.Ports[s.portID(dep.Port)]
		if p.InstanceID != i.ID || p.DeploymentID != dep.ID {
			return d.Fail("PORT_RESERVATION_LOST", "deployment no longer owns this port")
		}
		return nil
	})
	if err != nil {
		return dep, err
	}
	if dep.ExpiresAt != nil && !time.Now().Before(*dep.ExpiresAt) {
		return dep, d.Fail("SANDBOX_EXPIRED", "sandbox expired; delete it and create a new instance")
	}
	if dep.Digest != digest(dep.YAML) {
		return dep, d.Fail("ARTIFACT_DIGEST_MISMATCH", "saved deployment artifact changed")
	}
	if _, err = s.sync(ctx); err != nil {
		return dep, err
	}
	err = s.repo.View(ctx, func(st *d.State) error {
		p := st.Ports[s.portID(dep.Port)]
		if p.Conflict {
			return d.Fail("PORT_IN_USE", "foreign route occupies the reserved port")
		}
		return nil
	})
	if err != nil {
		return dep, err
	}
	if dep.Phase == "applied" || dep.Phase == "ready" {
		return s.Deployment(ctx, in)
	}
	if err = s.cluster.Preflight(ctx, i, rev.Parameters); err != nil {
		return dep, err
	}
	inventory, err := s.cluster.Inventory(ctx, dep.YAML)
	if err != nil {
		return dep, err
	}
	dep.Resources, err = mergeInventory(dep.Resources, inventory)
	if err != nil {
		return dep, err
	}
	if i.Software == "sandbox" && dep.ExpiresAt == nil {
		expires := time.Now().UTC().Add(time.Duration(rev.Parameters.TTLSeconds) * time.Second)
		dep.ExpiresAt = &expires
	}
	dep.Phase = "applying"
	dep.Stage = "dry-run"
	dep.Attempts++
	dep.LastError = ""
	if err = s.persist(ctx, dep); err != nil {
		return dep, err
	}
	fail := func(stage string, cause error) (d.Deployment, error) {
		dep.Phase = "unknown"
		dep.LastError = stage
		dep.Stage = stage
		saveErr := s.persist(ctx, dep)
		if saveErr != nil {
			return dep, saveErr
		}
		_ = cause
		return dep, d.Fail("DEPLOYMENT_RESULT_UNKNOWN", "deployment did not finish; reservation retained; inspect deployment and retry the same deploymentId")
	}
	if err = s.cluster.Apply(ctx, dep.YAML, i.Namespace, true); err != nil {
		return fail("dry-run", err)
	}
	dep.Stage = "gateway"
	if err = s.persist(ctx, dep); err != nil {
		return dep, err
	}
	if err = s.cluster.EnsurePort(ctx, s.gateway, dep.Port, listenerName(dep.Port)); err != nil {
		return fail("gateway", err)
	}
	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	err = s.repo.Update(recordCtx, func(st *d.State) error {
		p := st.Ports[s.portID(dep.Port)]
		p.ExposureManaged = true
		st.Ports[p.ID] = p
		return nil
	})
	recordCancel()
	if err != nil {
		return fail("gateway-recording", err)
	}
	dep.Stage = "resources"
	if err = s.persist(ctx, dep); err != nil {
		return dep, err
	}
	applyErr := s.cluster.Apply(ctx, dep.YAML, i.Namespace, false)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	inventory, err = s.cluster.Inventory(cleanup, dep.YAML)
	if err == nil {
		dep.Resources, err = mergeInventory(dep.Resources, inventory)
	}
	if err != nil {
		return fail("inventory", err)
	}
	if applyErr != nil {
		return fail("resources", applyErr)
	}
	dep.Observation, err = s.cluster.Inspect(ctx, dep, s.gateway)
	if err != nil {
		return fail("observation", err)
	}
	dep.Phase = "applied"
	dep.Stage = "observed"
	if ready(dep.Observation) {
		dep.Phase = "ready"
	}
	err = s.repo.Update(cleanup, func(st *d.State) error {
		dep.UpdatedAt = time.Now().UTC()
		st.Deployments[dep.ID] = dep
		p := st.Ports[s.portID(dep.Port)]
		p.State = "occupied"
		p.Exposed = true
		st.Ports[p.ID] = p
		s.event(st, p, "occupied", "", "")
		return nil
	})
	return dep, err
}
func ready(o d.Observation) bool {
	return o.WorkloadReady && o.PVCBound && o.RouteReady && o.GatewayReady && len(o.Missing) == 0
}
func (s *Service) DeleteInstance(ctx context.Context, in DeleteInput) (d.Deployment, error) {
	if err := s.policy(ctx, "instance.delete", true, in); err != nil {
		return d.Deployment{}, err
	}
	if !validRequest(in.RequestID) {
		return d.Deployment{}, d.Fail("MISSING_PARAMETER", "requestId is required")
	}
	unlock := s.lock(in.InstanceID)
	defer unlock()
	s.gatewayLock.Lock()
	defer s.gatewayLock.Unlock()
	var dep d.Deployment
	var i d.Instance
	err := s.repo.View(ctx, func(st *d.State) error {
		var ok bool
		i, ok = st.Instances[in.InstanceID]
		if !ok {
			return d.Fail("NOT_FOUND", "instance does not exist")
		}
		if i.DeploymentID != "" {
			dep = st.Deployments[i.DeploymentID]
		}
		if dep.ID != "" && dep.GatewayID != s.gateway.ID {
			return d.Fail("GATEWAY_NOT_CONFIGURED", "instance deployment belongs to another gateway")
		}

		return nil
	})
	if err != nil {
		return dep, err
	}
	if i.Deleted {
		return dep, nil
	}
	if dep.ID == "" {
		hasPort := false
		err = s.repo.View(ctx, func(st *d.State) error {
			for _, p := range st.Ports {
				if p.InstanceID == i.ID && p.GatewayID == s.gateway.ID {
					hasPort = true
				}
			}
			return nil
		})
		if err != nil {
			return dep, err
		}
		var snapshot d.Snapshot
		if hasPort {
			snapshot, err = s.sync(ctx)
			if err != nil {
				return dep, err
			}
		}
		err = s.repo.Update(ctx, func(st *d.State) error {
			for id, p := range st.Ports {
				if p.InstanceID != i.ID || p.GatewayID != s.gateway.ID {
					continue
				}
				if p.Conflict || len(snapshot.Ports[p.Port].Refs) > 0 {
					return d.Fail("PORT_IN_USE", "an unrecorded route occupies this instance reservation; reconcile it before deletion")
				}
				s.event(st, p, "instance_release", in.RequestID, digest(in))
				now := time.Now().UTC()
				st.Ports[id] = d.Port{ID: p.ID, GatewayID: p.GatewayID, GatewayFingerprint: p.GatewayFingerprint, ExposureManaged: p.ExposureManaged, Protocol: "TCP", Port: p.Port, State: "free", Exposed: p.Exposed, Listener: p.Listener, ReleasedAt: &now}
			}
			i = st.Instances[i.ID]
			i.Deleted = true
			st.Instances[i.ID] = i
			return nil
		})
		return dep, err
	}
	current, err := s.cluster.Inventory(ctx, dep.YAML)
	if err != nil {
		return dep, err
	}
	dep.Resources, err = mergeInventory(dep.Resources, current)
	if err != nil {
		return dep, err
	}
	dep.Phase = "deleting"
	dep.Stage = "deleting"
	dep.LastError = ""
	dep.Attempts++
	if err = s.repo.Update(ctx, func(st *d.State) error {
		dep.UpdatedAt = time.Now().UTC()
		st.Deployments[dep.ID] = dep
		p := st.Ports[s.portID(dep.Port)]
		p.State = "releasing"
		st.Ports[p.ID] = p
		return nil
	}); err != nil {
		return dep, err
	}
	fail := func(stage string) (d.Deployment, error) {
		dep.Phase = "unknown"
		dep.LastError = stage
		if err := s.persist(ctx, dep); err != nil {
			return dep, err
		}
		return dep, d.Fail("DELETE_PENDING", "cleanup is incomplete; port retained; retry deletion to reconcile")
	}
	// Remove the route first, then workloads. Never delete shared Gateway or LB.
	ordered := append([]d.Resource{}, dep.Resources...)
	for _, kind := range []string{"TCPRoute", "Deployment", "StatefulSet", "Service", "ConfigMap"} {
		for _, r := range ordered {
			if r.Kind == kind && r.UID != "" {
				if err = s.cluster.Delete(ctx, r); err != nil {
					return fail("delete-resource")
				}
			}
		}
	}
	actual, err := s.cluster.Inventory(ctx, dep.YAML)
	if err != nil {
		return fail("delete-observation")
	}
	for _, r := range actual {
		if r.Kind != "PersistentVolumeClaim" && r.UID != "" {
			return fail("delete-observation")
		}
	}
	snap, err := s.sync(ctx)
	if err != nil {
		return fail("delete-gateway-observation")
	}
	if len(snap.Ports[dep.Port].Refs) > 0 {
		return fail("delete-route-still-bound")
	}
	dep.Phase = "deleted"
	dep.Stage = "deleted"
	dep.Observation = d.Observation{Resources: actual, Note: "PVC retained. Shared Service and Gateway retained. Connectivity was not tested."}
	err = s.repo.Update(ctx, func(st *d.State) error {
		dep.UpdatedAt = time.Now().UTC()
		st.Deployments[dep.ID] = dep
		i = st.Instances[i.ID]
		i.Deleted = true
		st.Instances[i.ID] = i
		p := st.Ports[s.portID(dep.Port)]
		s.event(st, p, "instance_release", in.RequestID, digest(in))
		now := time.Now().UTC()
		st.Ports[p.ID] = d.Port{ExposureManaged: p.ExposureManaged, GatewayFingerprint: p.GatewayFingerprint, ID: p.ID, GatewayID: p.GatewayID, Protocol: "TCP", Port: p.Port, State: "free", Exposed: p.Exposed, Listener: p.Listener, ReleasedAt: &now}
		return nil
	})
	return dep, err
}

// VerifyAccess performs the selected software's fixed read-only protocol check from
// this server. It never claims that the user's own network can reach the LB.
func (s *Service) VerifyAccess(ctx context.Context, in DeploymentInput) (d.AccessReport, error) {
	if err := s.policy(ctx, "access.verify", false, in); err != nil {
		return d.AccessReport{}, err
	}
	dep, err := s.getDeployment(ctx, in.DeploymentID)
	if err != nil {
		return d.AccessReport{}, err
	}
	unlock := s.lock(dep.InstanceID)
	defer unlock()
	dep, err = s.getDeployment(ctx, in.DeploymentID)
	if err != nil {
		return d.AccessReport{}, err
	}
	var i d.Instance
	var revision d.Revision
	err = s.repo.View(ctx, func(st *d.State) error {
		i = st.Instances[dep.InstanceID]
		revision = st.Revisions[fmt.Sprintf("%s:%d", i.ID, dep.Revision)]
		if i.Deleted || i.DeploymentID != dep.ID || dep.Phase != "applied" && dep.Phase != "ready" {
			return d.Fail("DEPLOYMENT_INACTIVE", "protocol verification requires the current applied deployment")
		}
		return nil
	})
	if err != nil {
		return d.AccessReport{}, err
	}
	return s.cluster.Verify(ctx, s.gateway, dep, i, revision.Parameters)
}
