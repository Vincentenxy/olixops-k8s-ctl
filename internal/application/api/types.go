// Package api defines the typed application contract shared by HTTP and MCP.
package api

import (
	"context"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/domain"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

type EmptyInput struct{}

type NamespaceInput struct {
	Namespace string `json:"namespace" jsonschema:"Namespace within the configured allowlist"`
}

type RolloutInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind" jsonschema:"Deployment, StatefulSet or DaemonSet"`
	Name      string `json:"name"`
}

type ManifestInput struct {
	Namespace string `json:"namespace"`
	Manifest  string `json:"manifest" jsonschema:"YAML to validate locally; never applied to the cluster"`
}

type ItemsOutput[T any] struct {
	Items []T `json:"items"`
}

type ManifestOutput struct {
	Valid     bool                           `json:"valid"`
	Resources []application.ManifestResource `json:"resources"`
}

type ReadService interface {
	Status(context.Context) (clusterk8s.ClusterStatus, error)
	ListNamespaces(context.Context) ([]clusterk8s.NamespaceSummary, error)
	ListStorageClasses(context.Context) ([]clusterk8s.StorageClassSummary, error)
	ListResources(context.Context, string) (clusterk8s.NamespaceResources, error)
	ListGatewayResources(context.Context, string) (clusterk8s.GatewaySnapshot, error)
	GetRolloutStatus(context.Context, string, string, string) (clusterk8s.RolloutStatus, error)
	ValidateManifest(context.Context, string, string) ([]application.ManifestResource, error)
}

type Service interface {
	ReadService
	GetWorkloadHistory(context.Context, string, string, string, string, int64) (clusterk8s.WorkloadHistory, error)
	GetResource(context.Context, string, string, string) (clusterk8s.ResourceDetail, error)
	GetResourceYAML(context.Context, string, string, string) (clusterk8s.ResourceYAML, error)
	ListResourceObjects(context.Context, string, string, string, string, int64) (clusterk8s.ResourcePage, error)
	ListEvents(context.Context, string, string, string, string, int64) (clusterk8s.EventPage, error)
	GetPodLogs(context.Context, clusterk8s.LogOptions) (clusterk8s.PodLogs, error)
	ListHelmReleases(context.Context, string, int, int) ([]execctl.HelmRelease, error)
	GetHelmStatus(context.Context, string, string) (execctl.HelmRelease, error)
	GetHelmHistory(context.Context, string, string, int) ([]execctl.HelmRevision, error)
	ApplyManifest(context.Context, string, string, bool, bool) (execctl.Result, []application.ManifestResource, error)
	UpgradeHelm(context.Context, application.HelmUpgradeInput) (execctl.Result, error)
	ScaleWorkload(context.Context, string, string, string, int32) (clusterk8s.MutationResult, error)
	RestartWorkload(context.Context, string, string, string) (clusterk8s.MutationResult, error)
	RollbackWorkload(context.Context, string, string, string, int) (execctl.Result, error)
	DeleteResource(context.Context, string, string, string) (clusterk8s.MutationResult, error)
	RollbackHelm(context.Context, string, string, int, bool) (execctl.Result, error)
	UninstallHelm(context.Context, string, string, bool) (execctl.Result, error)
	CreateNamespace(context.Context, string) (clusterk8s.MutationResult, error)
	DeleteNamespace(context.Context, string) (clusterk8s.MutationResult, error)
	UpdateNamespaceMetadata(context.Context, string, application.NamespaceMetadataChanges) (clusterk8s.MutationResult, error)
	UpdateConfigMap(context.Context, string, string, map[string]string) (clusterk8s.MutationResult, error)
}

type ResourceInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

type ResourceYAMLInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"Namespace for namespaced resources; omit for Namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

type ListInput struct {
	Namespace     string `json:"namespace"`
	Kind          string `json:"kind"`
	LabelSelector string `json:"labelSelector,omitempty"`
	Continue      string `json:"continue,omitempty"`
	Limit         int64  `json:"limit,omitempty" jsonschema:"Page size 1-200, default 100"`
}

type EventsInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Continue  string `json:"continue,omitempty"`
	Limit     int64  `json:"limit,omitempty"`
}

type HelmListInput struct {
	Namespace string `json:"namespace"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

type HelmReleaseInput struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
}

type HelmHistoryInput struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
	Limit     int    `json:"limit,omitempty"`
}

type HistoryInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Continue  string `json:"continue,omitempty"`
	Limit     int64  `json:"limit,omitempty"`
}

type ApplyInput struct {
	Namespace  string `json:"namespace"`
	Manifest   string `json:"manifest"`
	ServerSide bool   `json:"serverSide,omitempty"`
}

type ApplyOutput struct {
	Resources []application.ManifestResource `json:"resources"`
	Result    execctl.Result                 `json:"result"`
	DryRun    bool                           `json:"dryRun"`
}

type ScaleInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind" jsonschema:"Deployment or StatefulSet"`
	Name      string `json:"name"`
	Replicas  int32  `json:"replicas" jsonschema:"Desired replicas from 0 to 10000"`
}

type RollbackInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Revision  int    `json:"revision" jsonschema:"Explicit positive target revision"`
}

type HelmRollbackInput struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
	Revision  int    `json:"revision"`
	Wait      bool   `json:"wait,omitempty"`
}

type HelmUninstallInput struct {
	Namespace string `json:"namespace"`
	Release   string `json:"release"`
	Wait      bool   `json:"wait,omitempty"`
}

type ConfigMapInput struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Data      map[string]string `json:"data" jsonschema:"Merge these keys into an existing ConfigMap, without deleting other keys"`
}

type NamespaceMetadataInput struct {
	Namespace         string            `json:"namespace" jsonschema:"Namespace within the configured allowlist"`
	Labels            map[string]string `json:"labels,omitempty" jsonschema:"Labels to set or replace"`
	Annotations       map[string]string `json:"annotations,omitempty" jsonschema:"Annotations to set or replace"`
	RemoveLabels      []string          `json:"removeLabels,omitempty" jsonschema:"Label keys to remove"`
	RemoveAnnotations []string          `json:"removeAnnotations,omitempty" jsonschema:"Annotation keys to remove"`
}

// TerminalService is the shared terminal and transfer-preparation application port.
type TerminalService interface {
	OpenPodTerminal(context.Context, domain.TerminalOpenInput) (domain.TerminalResult, error)
	WritePodTerminal(context.Context, domain.TerminalWriteInput) (domain.TerminalResult, error)
	ReadPodTerminal(context.Context, domain.TerminalReadInput) (domain.TerminalResult, error)
	ClosePodTerminal(context.Context, domain.TerminalSessionInput) (domain.TerminalResult, error)
	DownloadPodFile(context.Context, domain.DownloadInput) (domain.DownloadOutput, error)
	UploadPodFile(context.Context, domain.UploadInput) (domain.UploadOutput, error)
}
