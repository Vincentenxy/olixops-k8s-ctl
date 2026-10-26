// Package catalog defines the standard-service catalog and its external ports.
package catalog

import (
	"context"
	"fmt"
	"time"
)

const InstanceLabel = "olixops.io/instance-id"
const RevisionAnnotation = "olixops.io/revision"

type Error struct {
	Code    string `json:"reason"`
	Message string `json:"message"`
}

func (e *Error) Error() string        { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
func Fail(code, message string) error { return &Error{code, message} }

type Parameters struct {
	Image            string `json:"image,omitempty"`
	CPU              string `json:"cpu,omitempty"`
	Memory           string `json:"memory,omitempty"`
	Storage          string `json:"storage,omitempty"`
	StorageClass     string `json:"storageClass,omitempty"`
	SecretName       string `json:"secretName,omitempty"`
	SecretKey        string `json:"secretKey,omitempty"`
	Database         string `json:"database,omitempty"`
	Username         string `json:"username,omitempty"`
	Content          string `json:"content,omitempty"`
	RuntimeClassName string `json:"runtimeClassName,omitempty"`
	TTLSeconds       int    `json:"ttlSeconds,omitempty"`
}
type Template struct {
	ID               string     `json:"id"`
	Software         string     `json:"software"`
	Version          int        `json:"version"`
	Defaults         Parameters `json:"defaults"`
	ParameterSchema  string     `json:"parameterSchema"`
	ManifestTemplate string     `json:"manifestTemplate"`
	CreatedAt        time.Time  `json:"createdAt"`
}
type Instance struct {
	ID              string    `json:"id"`
	OwnerID         string    `json:"ownerId"`
	Name            string    `json:"name"`
	ResourceName    string    `json:"resourceName"`
	Namespace       string    `json:"namespace"`
	Software        string    `json:"software"`
	CurrentRevision int       `json:"currentRevision"`
	DeploymentID    string    `json:"deploymentId,omitempty"`
	Deleted         bool      `json:"deleted"`
	CreatedAt       time.Time `json:"createdAt"`
}
type Revision struct {
	StandardRequestDigest string     `json:"standardRequestDigest,omitempty"`
	ID                    string     `json:"id"`
	InstanceID            string     `json:"instanceId"`
	Number                int        `json:"revision"`
	TemplateID            string     `json:"templateId"`
	Parameters            Parameters `json:"parameters"`
	YAML                  string     `json:"yaml"`
	Digest                string     `json:"digest"`
	RequestID             string     `json:"requestId"`
	RequestDigest         string     `json:"requestDigest"`
	CreatedAt             time.Time  `json:"createdAt"`
}
type Resource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
}
type Observation struct {
	Address             string     `json:"address,omitempty"`
	Resources           []Resource `json:"resources"`
	Missing             []Resource `json:"missing"`
	WorkloadReady       bool       `json:"workloadReady"`
	PVCBound            bool       `json:"pvcBound"`
	RouteReady          bool       `json:"routeReady"`
	GatewayReady        bool       `json:"gatewayReady"`
	TCPVerified         bool       `json:"tcpVerified"`
	ProtocolVerified    bool       `json:"protocolVerified"`
	UserNetworkVerified bool       `json:"userNetworkVerified"`
	Note                string     `json:"note"`
}
type Deployment struct {
	ID            string      `json:"id"`
	InstanceID    string      `json:"instanceId"`
	Revision      int         `json:"revision"`
	Port          int         `json:"port"`
	GatewayID     string      `json:"gatewayId"`
	YAML          string      `json:"yaml"`
	Digest        string      `json:"digest"`
	RequestID     string      `json:"requestId"`
	RequestDigest string      `json:"requestDigest"`
	Phase         string      `json:"phase"`
	Stage         string      `json:"stage"`
	Attempts      int         `json:"attempts"`
	Resources     []Resource  `json:"resources"`
	Observation   Observation `json:"observation"`
	LastError     string      `json:"lastError,omitempty"`
	CreatedAt     time.Time   `json:"createdAt"`
	UpdatedAt     time.Time   `json:"updatedAt"`
	ExpiresAt     *time.Time  `json:"expiresAt,omitempty"`
}
type RouteMapping struct {
	Namespace        string `json:"namespace"`
	Route            string `json:"route"`
	Service          string `json:"service"`
	ServiceNamespace string `json:"serviceNamespace"`
	BackendKind      string `json:"backendKind"`
	BackendPort      int    `json:"backendPort"`
	InstanceID       string `json:"instanceId,omitempty"`
}

type Port struct {
	RouteMappings      []RouteMapping `json:"routeMappings,omitempty"`
	ExposureManaged    bool           `json:"exposureManaged"`
	GatewayFingerprint string         `json:"gatewayFingerprint"`
	ID                 string         `json:"id"`
	GatewayID          string         `json:"gatewayId"`
	Protocol           string         `json:"protocol"`
	Port               int            `json:"port"`
	State              string         `json:"state"`
	Exposed            bool           `json:"exposed"`
	Listener           string         `json:"listener,omitempty"`
	OwnerID            string         `json:"ownerId,omitempty"`
	InstanceID         string         `json:"instanceId,omitempty"`
	DeploymentID       string         `json:"deploymentId,omitempty"`
	Namespace          string         `json:"namespace,omitempty"`
	Service            string         `json:"service,omitempty"`
	BackendPort        int            `json:"backendPort,omitempty"`
	Route              string         `json:"route,omitempty"`
	ExternalRefs       []string       `json:"externalRefs,omitempty"`
	Conflict           bool           `json:"conflict"`
	Note               string         `json:"note,omitempty"`
	ReservedAt         *time.Time     `json:"reservedAt,omitempty"`
	ReleasedAt         *time.Time     `json:"releasedAt,omitempty"`
}
type PortEvent struct {
	PortSnapshot  *Port     `json:"portSnapshot,omitempty"`
	ID            string    `json:"id"`
	Port          int       `json:"port"`
	GatewayID     string    `json:"gatewayId"`
	Action        string    `json:"action"`
	InstanceID    string    `json:"instanceId,omitempty"`
	DeploymentID  string    `json:"deploymentId,omitempty"`
	RequestID     string    `json:"requestId,omitempty"`
	RequestDigest string    `json:"requestDigest,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// State is a small transactional aggregate. SQLite uniquely keys each gateway/TCP/port.
// Network I/O must never occur inside Repository.Update callbacks.
type State struct {
	Templates   map[string]Template
	Instances   map[string]Instance
	Revisions   map[string]Revision
	Deployments map[string]Deployment
	Ports       map[string]Port
	Events      map[string]PortEvent
}

func NewState() *State {
	return &State{map[string]Template{}, map[string]Instance{}, map[string]Revision{}, map[string]Deployment{}, map[string]Port{}, map[string]PortEvent{}}
}

type Repository interface {
	View(context.Context, func(*State) error) error
	Update(context.Context, func(*State) error) error
}
type Gateway struct {
	ID        string `json:"id"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Name      string `json:"name"`
	PortStart int    `json:"portStart"`
	PortEnd   int    `json:"portEnd"`
}
type PortUsage struct {
	Mappings []RouteMapping
	Port     int
	Exposed  bool
	Listener string
	Refs     []string
	// Owners identify all matching routes, including empty IDs for foreign routes.
	Owners []string
}
type Snapshot struct {
	Ports           map[int]PortUsage
	TCPRouteVersion string
}

// Cluster isolates client-go and kubectl. Check validates namespace/resource scope
// locally; Apply always includes server dry-run before a real submission.
type Cluster interface {
	Verify(context.Context, Gateway, Deployment, Instance, Parameters) (AccessReport, error)
	Check(context.Context, string, string) error
	Snapshot(context.Context, Gateway) (Snapshot, error)
	// Preflight ensures the explicit Namespace exists, then checks storage and credentials.
	Preflight(context.Context, Instance, Parameters) error
	EnsurePort(context.Context, Gateway, int, string) error
	Apply(context.Context, string, string, bool) error
	Inventory(context.Context, string) ([]Resource, error)
	Inspect(context.Context, Deployment, Gateway) (Observation, error)
	Delete(context.Context, Resource) error
}

// AccessReport separates server-side protocol checks from the user's network.
type AccessReport struct {
	Address             string    `json:"address"`
	Port                int       `json:"port"`
	TCPVerified         bool      `json:"tcpVerified"`
	ProtocolVerified    bool      `json:"protocolVerified"`
	UserNetworkVerified bool      `json:"userNetworkVerified"`
	CheckedAt           time.Time `json:"checkedAt"`
	Result              string    `json:"result"`
}
