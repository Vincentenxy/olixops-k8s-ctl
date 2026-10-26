// Package catalog implements standard service workflows shared by HTTP and MCP.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"olixops-k8s-ctl/internal/domain"
	d "olixops-k8s-ctl/internal/domain/catalog"
	"sigs.k8s.io/yaml"
)

type Service struct {
	repo       d.Repository
	cluster    d.Cluster
	gateway    d.Gateway
	authorizer domain.Authorizer
	defaults   Defaults
	locks      sync.Map
	// A sync and a deploy must not race in this single-replica coordinator.
	gatewayLock sync.Mutex
}

func New(ctx context.Context, repo d.Repository, cluster d.Cluster, gateway d.Gateway, authorizer domain.Authorizer, options ...Option) (*Service, error) {
	if gateway.ID == "" || gateway.Cluster == "" || len(validation.IsDNS1123Label(gateway.Namespace)) > 0 || len(validation.IsDNS1123Subdomain(gateway.Name)) > 0 || len(validation.IsDNS1123Subdomain(gateway.Service)) > 0 || gateway.PortStart < 1 || gateway.PortEnd > 65535 || gateway.PortEnd < gateway.PortStart || gateway.PortEnd-gateway.PortStart > 1000 {
		return nil, fmt.Errorf("invalid catalog gateway configuration")
	}
	if authorizer == nil {
		authorizer = domain.DeferredAuthorizer{}
	}
	s := &Service{repo: repo, cluster: cluster, gateway: gateway, authorizer: authorizer}
	for _, option := range options {
		option(s)
	}
	err := repo.Update(ctx, func(st *d.State) error {
		for _, t := range seeds() {
			if _, ok := st.Templates[t.ID]; !ok {
				st.Templates[t.ID] = t
			}
		}
		for p := gateway.PortStart; p <= gateway.PortEnd; p++ {
			key := s.portID(p)
			if old, ok := st.Ports[key]; ok && old.GatewayFingerprint != "" && old.GatewayFingerprint != digest(gateway) {
				return fmt.Errorf("catalog gateway identity changed; restore its configuration or use a new gateway-id")
			}
			if _, ok := st.Ports[key]; !ok {
				st.Ports[key] = d.Port{GatewayFingerprint: digest(gateway), ID: key, GatewayID: gateway.ID, Protocol: "TCP", Port: p, State: "unchecked"}
			}
		}
		// A crash can leave an operation in progress. Preserve reservations and inventory.
		for id, dep := range st.Deployments {
			if dep.Phase == "applying" || dep.Phase == "deleting" {
				dep.Phase = "unknown"
				dep.LastError = "PROCESS_INTERRUPTED"
				dep.UpdatedAt = time.Now().UTC()
				st.Deployments[id] = dep
			}
		}
		return nil
	})
	return s, err
}
func (s *Service) policy(ctx context.Context, action string, write bool, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// ownerId is metadata. Future identity comes only from trusted ctx middleware.
	return s.authorizer.Authorize(ctx, domain.AuthorizationRequest{Cluster: s.gateway.Cluster, Action: "catalog." + action, Write: write, PayloadDigest: digest(payload)})
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func sortBy[T any](a []T, key func(T) string) {
	sort.Slice(a, func(i, j int) bool { return key(a[i]) < key(a[j]) })
}
func (s *Service) lock(id string) func() {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func (s *Service) portID(p int) string { return fmt.Sprintf("%s:TCP:%d", s.gateway.ID, p) }
func stamp(raw, id string, revision int) (string, error) {
	dec := kyaml.NewYAMLOrJSONDecoder(strings.NewReader(raw), 4096)
	var docs []string
	for {
		var obj map[string]any
		err := dec.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", d.Fail("INVALID_TEMPLATE", "rendered YAML is invalid")
		}
		if len(obj) == 0 {
			continue
		}
		u := &unstructured.Unstructured{Object: obj}
		labels := u.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[d.InstanceLabel] = id
		u.SetLabels(labels)
		ann := u.GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		ann[d.RevisionAnnotation] = strconv.Itoa(revision)
		u.SetAnnotations(ann)
		if u.GetKind() == "StatefulSet" || u.GetKind() == "Deployment" {
			podAnn, _, _ := unstructured.NestedStringMap(u.Object, "spec", "template", "metadata", "annotations")
			if podAnn == nil {
				podAnn = map[string]string{}
			}
			podAnn[d.RevisionAnnotation] = strconv.Itoa(revision)
			if err := unstructured.SetNestedStringMap(u.Object, podAnn, "spec", "template", "metadata", "annotations"); err != nil {
				return "", err
			}
		}
		b, err := yaml.Marshal(u.Object)
		if err != nil {
			return "", err
		}
		docs = append(docs, string(b))
	}
	return strings.Join(docs, "---\n"), nil
}

type SaveConfigInput struct {
	StandardRequestDigest string       `json:"-"`
	InstanceID            string       `json:"instanceId,omitempty"`
	OwnerID               string       `json:"ownerId"`
	Name                  string       `json:"name"`
	Namespace             string       `json:"namespace"`
	Software              string       `json:"software"`
	TemplateVersion       int          `json:"templateVersion"`
	Parameters            d.Parameters `json:"parameters"`
	ExpectedRevision      int          `json:"expectedRevision"`
	RequestID             string       `json:"requestId,omitempty"`
}
type SavedConfig struct {
	Instance d.Instance `json:"instance"`
	Revision d.Revision `json:"revision"`
}
type InstanceInput struct {
	InstanceID string `json:"instanceId"`
}
type DeploymentInput struct {
	DeploymentID string `json:"deploymentId"`
}
type ListInput struct {
	OwnerID    string `json:"ownerId,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Offset     int    `json:"offset,omitempty"`
}

func validRequest(id string) bool { return len(id) > 0 && len(id) <= 128 }
func (s *Service) build(ctx context.Context, st *d.State, in SaveConfigInput, preview bool) (SavedConfig, error) {
	var out SavedConfig
	if (!preview && !validRequest(in.RequestID)) || in.OwnerID == "" || len(in.OwnerID) > 128 || len(validation.IsDNS1123Label(in.Name)) > 0 || len(in.Name) > 36 || len(validation.IsDNS1123Label(in.Namespace)) > 0 || !softwareValid(in.Software) {
		return out, d.Fail("MISSING_PARAMETER", "supply ownerId, name (DNS label, up to 36 characters), namespace, software, templateVersion and requestId")
	}
	t, ok := st.Templates[templateID(in.Software, in.TemplateVersion)]
	if !ok {
		return out, d.Fail("NOT_FOUND", "template version does not exist")
	}
	i := d.Instance{ID: uuid.NewString(), OwnerID: in.OwnerID, Name: in.Name, Namespace: in.Namespace, Software: in.Software, CreatedAt: time.Now().UTC()}
	if in.InstanceID != "" {
		var exists bool
		i, exists = st.Instances[in.InstanceID]
		if !exists {
			return out, d.Fail("NOT_FOUND", "instance does not exist")
		}
		if i.Deleted {
			return out, d.Fail("INSTANCE_DELETED", "create a new instance to deploy again")
		}
		if i.OwnerID != in.OwnerID || i.Name != in.Name || i.Namespace != in.Namespace || i.Software != in.Software {
			return out, d.Fail("INSTANCE_IDENTITY_CONFLICT", "owner, name, namespace and software are immutable")
		}
	}
	if i.CurrentRevision != in.ExpectedRevision {
		return out, d.Fail("REVISION_CONFLICT", "expectedRevision does not match the current revision")
	}
	if i.ResourceName == "" {
		i.ResourceName = in.Name + "-" + i.ID[:8]
	}
	p := merge(t.Defaults, in.Parameters)
	if err := validateParams(in.Software, p); err != nil {
		return out, err
	}
	profile, _ := d.Profile(in.Software)
	if i.CurrentRevision > 0 && profile.Persistent {
		old := st.Revisions[fmt.Sprintf("%s:%d", i.ID, i.CurrentRevision)]
		a, _ := resource.ParseQuantity(old.Parameters.Storage)
		b, _ := resource.ParseQuantity(p.Storage)
		if b.Cmp(a) < 0 {
			return out, d.Fail("STORAGE_SHRINK_FORBIDDEN", "PVC storage cannot shrink")
		}
		if p.Image != old.Parameters.Image {
			return out, d.Fail("IMAGE_CHANGE_REQUIRES_MIGRATION", "database image changes require a separate migration workflow")
		}
		if p.StorageClass != old.Parameters.StorageClass || p.Database != old.Parameters.Database || p.Username != old.Parameters.Username {
			return out, d.Fail("IMMUTABLE_PARAMETER", "storageClass, database and username cannot change after creation")
		}
	}
	rev := i.CurrentRevision + 1
	raw, err := render(t, i, p, rev)
	if err != nil {
		return out, err
	}
	if err = s.cluster.Check(ctx, raw, i.Namespace); err != nil {
		return out, err
	}
	r := d.Revision{StandardRequestDigest: in.StandardRequestDigest, ID: fmt.Sprintf("%s:%d", i.ID, rev), InstanceID: i.ID, Number: rev, TemplateID: t.ID, Parameters: p, YAML: raw, Digest: digest(raw), RequestID: in.RequestID, RequestDigest: digest(in), CreatedAt: time.Now().UTC()}
	i.CurrentRevision = rev
	return SavedConfig{i, r}, nil
}
func (s *Service) PreviewConfig(ctx context.Context, in SaveConfigInput) (SavedConfig, error) {
	in.Software = d.NormalizeSoftware(in.Software)
	var out SavedConfig
	if err := s.policy(ctx, "config.preview", false, in); err != nil {
		return out, err
	}
	err := s.repo.View(ctx, func(st *d.State) error { var err error; out, err = s.build(ctx, st, in, true); return err })
	return out, err
}
func (s *Service) SaveConfig(ctx context.Context, in SaveConfigInput) (SavedConfig, error) {
	in.Software = d.NormalizeSoftware(in.Software)
	var out SavedConfig
	if err := s.policy(ctx, "config.save", true, in); err != nil {
		return out, err
	}
	err := s.repo.Update(ctx, func(st *d.State) error {
		for _, r := range st.Revisions {
			if r.RequestID == in.RequestID {
				if (in.StandardRequestDigest != "" && r.StandardRequestDigest != in.StandardRequestDigest) || (in.StandardRequestDigest == "" && (r.StandardRequestDigest != "" || r.RequestDigest != digest(in))) {
					return d.Fail("IDEMPOTENCY_CONFLICT", "requestId already used with different input")
				}
				out = SavedConfig{st.Instances[r.InstanceID], r}
				return nil
			}
		}
		var err error
		out, err = s.build(ctx, st, in, false)
		if err != nil {
			return err
		}
		st.Instances[out.Instance.ID] = out.Instance
		st.Revisions[out.Revision.ID] = out.Revision
		return nil
	})
	return out, err
}
func (s *Service) Instances(ctx context.Context, in ListInput) ([]d.Instance, error) {
	out := []d.Instance{}
	if err := s.policy(ctx, "instance.list", false, in); err != nil {
		return nil, err
	}
	err := s.repo.View(ctx, func(st *d.State) error {
		for _, i := range st.Instances {
			if (in.OwnerID == "" || in.OwnerID == i.OwnerID) && (in.InstanceID == "" || in.InstanceID == i.ID) {
				out = append(out, i)
			}
		}
		return nil
	})
	sortBy(out, func(v d.Instance) string { return v.CreatedAt.Format(time.RFC3339Nano) + v.ID })
	return page(out, in), err
}
func (s *Service) Instance(ctx context.Context, in InstanceInput) (d.Instance, error) {
	var out d.Instance
	if err := s.policy(ctx, "instance.get", false, in); err != nil {
		return out, err
	}
	err := s.repo.View(ctx, func(st *d.State) error {
		var ok bool
		out, ok = st.Instances[in.InstanceID]
		if !ok {
			return d.Fail("NOT_FOUND", "instance does not exist")
		}
		return nil
	})
	return out, err
}
func (s *Service) Revisions(ctx context.Context, in ListInput) ([]d.Revision, error) {
	out := []d.Revision{}
	if err := s.policy(ctx, "revision.list", false, in); err != nil {
		return nil, err
	}
	err := s.repo.View(ctx, func(st *d.State) error {
		for _, r := range st.Revisions {
			if r.InstanceID == in.InstanceID {
				out = append(out, r)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return page(out, in), err
}
func page[T any](v []T, in ListInput) []T {
	limit := in.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	start := in.Offset
	if start < 0 {
		start = 0
	}
	if start >= len(v) {
		return []T{}
	}
	end := start + limit
	if end > len(v) {
		end = len(v)
	}
	return v[start:end]
}
