package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

// Defaults supplies environment choices, while immutable templates supply software defaults.
type Defaults struct {
	OwnerID          string `json:"ownerId"`
	StorageClass     string `json:"storageClass"`
	RuntimeClassName string `json:"runtimeClassName,omitempty"`
}
type Option func(*Service)

func WithDefaults(defaults Defaults) Option { return func(s *Service) { s.defaults = defaults } }

type StandardService struct {
	d.SoftwareProfile
	TemplateVersion int          `json:"templateVersion"`
	Parameters      d.Parameters `json:"parameters"`
}
type StandardServices struct {
	Defaults Defaults          `json:"defaults"`
	Items    []StandardService `json:"items"`
}

func (s *Service) StandardServices(ctx context.Context) (StandardServices, error) {
	out := StandardServices{Defaults: s.defaults, Items: []StandardService{}}
	if err := s.policy(ctx, "standard.list", false, nil); err != nil {
		return out, err
	}
	err := s.repo.View(ctx, func(st *d.State) error {
		for _, profile := range d.Profiles() {
			seed := seedsFor(profile.Software)
			t, ok := st.Templates[seed.ID]
			if !ok {
				return d.Fail("NOT_FOUND", "recommended template does not exist")
			}
			p := s.environmentParameters(profile, t.Defaults)
			out.Items = append(out.Items, StandardService{profile, t.Version, p})
		}
		return nil
	})
	return out, err
}
func (s *Service) environmentParameters(profile d.SoftwareProfile, p d.Parameters) d.Parameters {
	if profile.Persistent && s.defaults.StorageClass != "" {
		p.StorageClass = s.defaults.StorageClass
	}
	if profile.Software == "sandbox" && s.defaults.RuntimeClassName != "" {
		p.RuntimeClassName = s.defaults.RuntimeClassName
	}
	return p
}

type DeployStandardInput struct {
	Software        string       `json:"software"`
	RequestID       string       `json:"requestId"`
	Name            string       `json:"name,omitempty"`
	Namespace       string       `json:"namespace"`
	OwnerID         string       `json:"ownerId,omitempty"`
	TemplateVersion int          `json:"templateVersion,omitempty"`
	Parameters      d.Parameters `json:"parameters,omitempty"`
}
type StandardDeployment struct {
	RequestID       string       `json:"requestId"`
	Instance        d.Instance   `json:"instance"`
	Deployment      d.Deployment `json:"deployment"`
	Protocol        string       `json:"protocol"`
	InternalAddress string       `json:"internalAddress"`
	ExternalAddress string       `json:"externalAddress,omitempty"`
}

// DeployStandard composes the existing durable workflow. Repeating the same request
// resumes its saved revision/deployment instead of creating a second instance.
func (s *Service) DeployStandard(ctx context.Context, in DeployStandardInput) (StandardDeployment, error) {
	out := StandardDeployment{RequestID: in.RequestID}
	if err := s.policy(ctx, "standard.deploy", true, in); err != nil {
		return out, err
	}
	if strings.TrimSpace(in.Namespace) == "" {
		return out, d.Fail("MISSING_NAMESPACE", "请指定部署目标 Namespace（namespace）")
	}
	if len(validation.IsDNS1123Label(in.Namespace)) > 0 {
		return out, d.Fail("INVALID_NAMESPACE", "namespace must be a valid Kubernetes DNS label")
	}
	if !validRequest(in.RequestID) {
		return out, d.Fail("MISSING_PARAMETER", "requestId is required and must be at most 128 characters")
	}
	profile, ok := d.Profile(in.Software)
	if !ok {
		return out, d.Fail("INVALID_SOFTWARE", "unsupported software")
	}
	in.Software = profile.Software
	requestDigest := digest(in)
	if in.Name == "" {
		in.Name = in.Software + "-" + digest(in.RequestID)[:8]
	}
	if in.OwnerID == "" {
		in.OwnerID = s.defaults.OwnerID
	}
	if in.TemplateVersion == 0 {
		in.TemplateVersion = seedsFor(in.Software).Version
	}
	p := s.environmentParameters(profile, d.Parameters{})
	in.Parameters = merge(p, in.Parameters)
	saved, err := s.SaveConfig(ctx, SaveConfigInput{OwnerID: in.OwnerID, Name: in.Name, Namespace: in.Namespace, Software: in.Software, TemplateVersion: in.TemplateVersion, Parameters: in.Parameters, RequestID: in.RequestID, StandardRequestDigest: requestDigest})
	if err != nil {
		return out, err
	}
	out.Instance = saved.Instance
	out.Protocol = profile.Protocol
	out.InternalAddress = fmt.Sprintf("%s.%s.svc.cluster.local:%d", saved.Instance.ResourceName, saved.Instance.Namespace, profile.BackendPort)
	if profile.Protocol == "http" {
		out.InternalAddress = "http://" + out.InternalAddress
	}
	dep, err := s.Prepare(ctx, PrepareInput{InstanceID: saved.Instance.ID, Revision: saved.Revision.Number, RequestID: in.RequestID})
	out.Deployment = dep
	if err != nil {
		return out, err
	}
	dep, err = s.Deploy(ctx, DeploymentInput{DeploymentID: dep.ID})
	out.Deployment = dep
	if dep.Observation.Address != "" {
		out.ExternalAddress = dep.Observation.Address
		if profile.Protocol == "http" {
			out.ExternalAddress = "http://" + out.ExternalAddress
		}
	}
	return out, err
}

// ExpireSandboxes uses the same UID-aware deletion and confirmed port release as
// manual deletion. Expiration is persisted, so restarts do not reset a lease.
func (s *Service) ExpireSandboxes(ctx context.Context, now time.Time) error {
	var ids []string
	if err := s.repo.View(ctx, func(st *d.State) error {
		for _, i := range st.Instances {
			if i.Deleted || i.Software != "sandbox" {
				continue
			}
			dep := st.Deployments[i.DeploymentID]
			if dep.ExpiresAt != nil && !now.Before(*dep.ExpiresAt) {
				ids = append(ids, i.ID)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		if _, err := s.DeleteInstance(ctx, DeleteInput{InstanceID: id, RequestID: "expire-" + id}); err != nil && first == nil {
			first = err
		}
	}
	return first
}
