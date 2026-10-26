package catalog

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

type PortInput struct {
	Port   int    `json:"port,omitempty"`
	State  string `json:"state,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}
type ReserveInput struct {
	InstanceID string `json:"instanceId"`
	Port       int    `json:"port"`
	RequestID  string `json:"requestId"`
	Note       string `json:"note,omitempty"`
}
type ReleaseInput struct {
	Port      int    `json:"port"`
	RequestID string `json:"requestId"`
}
type PortList struct {
	Gateway d.Gateway `json:"gateway"`
	Items   []d.Port  `json:"items"`
}

func (s *Service) Ports(ctx context.Context, in PortInput) (PortList, error) {
	out := PortList{Gateway: s.gateway, Items: []d.Port{}}
	if err := s.policy(ctx, "port.list", false, in); err != nil {
		return out, err
	}
	err := s.repo.View(ctx, func(st *d.State) error {
		for _, p := range st.Ports {
			if p.GatewayID == s.gateway.ID && (in.Port == 0 || p.Port == in.Port) && (in.State == "" || p.State == in.State) {
				out.Items = append(out.Items, p)
			}
		}
		return nil
	})
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Port < out.Items[j].Port })
	out.Items = page(out.Items, ListInput{Limit: in.Limit, Offset: in.Offset})
	return out, err
}
func (s *Service) PortEvents(ctx context.Context, in PortInput) ([]d.PortEvent, error) {
	out := []d.PortEvent{}
	if err := s.policy(ctx, "port.history", false, in); err != nil {
		return nil, err
	}
	err := s.repo.View(ctx, func(st *d.State) error {
		for _, v := range st.Events {
			if v.GatewayID == s.gateway.ID && (in.Port == 0 || v.Port == in.Port) {
				out = append(out, v)
			}
		}
		return nil
	})
	sortBy(out, func(v d.PortEvent) string { return v.CreatedAt.Format(time.RFC3339Nano) + v.ID })
	return page(out, ListInput{Limit: in.Limit, Offset: in.Offset}), err
}
func (s *Service) event(st *d.State, p d.Port, action, req, reqDigest string) {
	v := d.PortEvent{PortSnapshot: &p, ID: uuid.NewString(), GatewayID: s.gateway.ID, Port: p.Port, Action: action, InstanceID: p.InstanceID, DeploymentID: p.DeploymentID, RequestID: req, RequestDigest: reqDigest, CreatedAt: time.Now().UTC()}
	st.Events[v.ID] = v
}
func listenerName(port int) string { return fmt.Sprintf("olixops-tcp-%d", port) }
func (s *Service) SyncPorts(ctx context.Context) (PortList, error) {
	if err := s.policy(ctx, "port.sync", true, nil); err != nil {
		return PortList{}, err
	}
	s.gatewayLock.Lock()
	defer s.gatewayLock.Unlock()
	if _, err := s.sync(ctx); err != nil {
		return PortList{}, err
	}
	return s.Ports(ctx, PortInput{Limit: 500})
}

// sync is serialized with all standard apply/delete/port changes. A partial read
// never frees a port, including a previously available port.
func (s *Service) sync(ctx context.Context) (d.Snapshot, error) {
	snap, err := s.cluster.Snapshot(ctx, s.gateway)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		saveErr := s.repo.Update(cleanup, func(st *d.State) error {
			for id, p := range st.Ports {
				if p.GatewayID == s.gateway.ID && (p.State == "free" || p.State == "unchecked") {
					p.State = "unchecked"
					st.Ports[id] = p
				}
			}
			return nil
		})
		if saveErr != nil {
			return snap, saveErr
		}
		return snap, d.Fail("GATEWAY_SYNC_INCOMPLETE", "cannot completely read Higress Service, Gateway and cluster-wide TCPRoutes; no port allocation is allowed")
	}
	err = s.repo.Update(ctx, func(st *d.State) error {
		for id, p := range st.Ports {
			if p.GatewayID != s.gateway.ID {
				continue
			}
			u := snap.Ports[p.Port]
			old := p
			p.Exposed = u.Exposed
			p.Listener = u.Listener
			p.ExternalRefs = u.Refs
			p.RouteMappings = u.Mappings
			if p.InstanceID == "" && len(u.Mappings) > 0 {
				mapping := u.Mappings[0]
				p.Namespace = mapping.Namespace
				p.Service = mapping.Service
				p.BackendPort = mapping.BackendPort
				p.Route = mapping.Route
			}
			p.Conflict = false
			foreign := len(u.Refs) != len(u.Owners)
			for n, owner := range u.Owners {
				if p.InstanceID == "" || owner != p.InstanceID || n >= len(u.Refs) || u.Refs[n] != p.Namespace+"/"+p.Service+"-route" {
					foreign = true
				}
			}
			for _, mapping := range u.Mappings {
				if mapping.Namespace != p.Namespace || mapping.Route != p.Service+"-route" || mapping.Service != p.Service || mapping.ServiceNamespace != p.Namespace || mapping.BackendPort != p.BackendPort || mapping.BackendKind != "Service" {
					foreign = true
				}
			}

			if p.InstanceID != "" {
				if foreign {
					p.Conflict = true
				}
			} else if len(u.Refs) > 0 || u.Listener != "" && (!p.ExposureManaged || u.Listener != listenerName(p.Port)) || u.Exposed && !p.ExposureManaged {
				p.State = "external"
			} else if p.State != "external" {
				p.State = "free"
			}
			if old.State != p.State || old.Conflict != p.Conflict || old.Listener != p.Listener || old.Exposed != p.Exposed || digest(old.RouteMappings) != digest(p.RouteMappings) || digest(old.ExternalRefs) != digest(p.ExternalRefs) {
				s.event(st, p, "sync", "", "")
			}
			st.Ports[id] = p
		}
		return nil
	})
	return snap, err
}
func (s *Service) port(st *d.State, port int) (d.Port, error) {
	p, ok := st.Ports[s.portID(port)]
	if !ok {
		return p, d.Fail("PORT_NOT_IN_POOL", fmt.Sprintf("external port must be between %d and %d", s.gateway.PortStart, s.gateway.PortEnd))
	}
	return p, nil
}
func (s *Service) claim(st *d.State, i d.Instance, port int, req string) (d.Port, error) {
	p, err := s.port(st, port)
	if err != nil {
		return p, err
	}
	for _, other := range st.Ports {
		if other.GatewayID == s.gateway.ID && other.InstanceID == i.ID && other.Port != port && (other.State == "reserved" || other.State == "occupied" || other.State == "releasing") {
			return p, d.Fail("INSTANCE_PORT_ALREADY_RESERVED", "this instance already owns another port; release its reservation or delete its deployment first")
		}
	}
	if p.Conflict {
		return p, d.Fail("PORT_IN_USE", "port has conflicting cluster routes")
	}
	if p.State == "unchecked" {
		return p, d.Fail("PORT_NOT_RECONCILED", "port pool requires a complete gateway sync")
	}
	if p.InstanceID == i.ID && (p.State == "reserved" || p.State == "occupied") {
		return p, nil
	}
	if p.State != "free" {
		return p, d.Fail("PORT_IN_USE", "port is reserved or occupied; inspect list_gateway_ports for existing usage")
	}
	now := time.Now().UTC()
	p.State = "reserved"
	p.OwnerID = i.OwnerID
	p.InstanceID = i.ID
	p.Namespace = i.Namespace
	p.Service = i.ResourceName
	p.BackendPort = backend(i.Software)
	p.ReservedAt = &now
	p.ReleasedAt = nil
	s.event(st, p, "reserve", req, "")
	st.Ports[p.ID] = p
	return p, nil
}
func (s *Service) ReservePort(ctx context.Context, in ReserveInput) (d.Port, error) {
	var out d.Port
	if err := s.policy(ctx, "port.reserve", true, in); err != nil {
		return out, err
	}
	if !validRequest(in.RequestID) {
		return out, d.Fail("MISSING_PARAMETER", "requestId is required")
	}
	s.gatewayLock.Lock()
	defer s.gatewayLock.Unlock()
	// Idempotent read first: a retry must not reacquire an already released port.
	found := false
	err := s.repo.View(ctx, func(st *d.State) error {
		for _, e := range st.Events {
			if e.Action == "manual_reserve" && e.RequestID == in.RequestID {
				if e.RequestDigest != digest(in) {
					return d.Fail("IDEMPOTENCY_CONFLICT", "requestId has different input")
				}
				out = st.Ports[s.portID(e.Port)]
				found = true
			}
		}
		return nil
	})
	if err != nil || found {
		return out, err
	}
	if _, err = s.sync(ctx); err != nil {
		return out, err
	}
	err = s.repo.Update(ctx, func(st *d.State) error {
		i, ok := st.Instances[in.InstanceID]
		if !ok || i.Deleted {
			return d.Fail("NOT_FOUND", "active instance does not exist")
		}
		var err error
		out, err = s.claim(st, i, in.Port, in.RequestID)
		if err != nil {
			return err
		}
		out.Note = in.Note
		st.Ports[out.ID] = out
		s.event(st, out, "manual_reserve", in.RequestID, digest(in))
		return nil
	})
	return out, err
}
func (s *Service) ReleasePort(ctx context.Context, in ReleaseInput) (d.Port, error) {
	var out d.Port
	if err := s.policy(ctx, "port.release", true, in); err != nil {
		return out, err
	}
	if !validRequest(in.RequestID) {
		return out, d.Fail("MISSING_PARAMETER", "requestId is required")
	}
	s.gatewayLock.Lock()
	defer s.gatewayLock.Unlock()
	snap, err := s.sync(ctx)
	if err != nil {
		return out, err
	}
	err = s.repo.Update(ctx, func(st *d.State) error {
		for _, e := range st.Events {
			if e.Action == "release" && e.RequestID == in.RequestID {
				if e.RequestDigest != digest(in) {
					return d.Fail("IDEMPOTENCY_CONFLICT", "requestId has different input")
				}
				out = st.Ports[s.portID(e.Port)]
				return nil
			}
		}
		p, err := s.port(st, in.Port)
		if err != nil {
			return err
		}
		u := snap.Ports[p.Port]
		if len(u.Refs) > 0 || p.Conflict {
			return d.Fail("PORT_IN_USE", "actual TCPRoute still references this port")
		}
		if p.State == "external" && (u.Exposed || u.Listener != "") {
			return d.Fail("PORT_IN_USE", "externally managed exposure must be removed before reclaiming")
		}
		for _, dep := range st.Deployments {
			instance := st.Instances[dep.InstanceID]
			if instance.Deleted || instance.DeploymentID != dep.ID {
				continue
			}
			if dep.Port == p.Port && dep.GatewayID == s.gateway.ID && dep.Phase != "deleted" && dep.Phase != "cancelled" {
				if dep.Phase != "prepared" {
					return d.Fail("DEPLOYMENT_ACTIVE", "delete or reconcile the instance before releasing its port")
				}
				for _, r := range dep.Resources {
					if r.UID != "" {
						return d.Fail("DEPLOYMENT_ACTIVE", "prepared update retains existing resources; delete the instance first")
					}
				}
				instance.DeploymentID = ""
				st.Instances[instance.ID] = instance
				dep.Phase = "cancelled"
				dep.UpdatedAt = time.Now().UTC()
				st.Deployments[dep.ID] = dep
			}
		}
		s.event(st, p, "release", in.RequestID, digest(in))
		now := time.Now().UTC()
		out = d.Port{ExposureManaged: p.ExposureManaged, GatewayFingerprint: p.GatewayFingerprint, ID: p.ID, GatewayID: p.GatewayID, Protocol: "TCP", Port: p.Port, State: "free", Exposed: p.Exposed, Listener: p.Listener, ReleasedAt: &now}
		st.Ports[p.ID] = out
		return nil
	})
	return out, err
}
