package handler

import (
	"context"
	"github.com/gin-gonic/gin"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/application/api"
	"time"
)

type ClusterHandler struct {
	operations   api.Operations
	writeTimeout time.Duration
}

func NewClusterHandler(service api.Service, timeout ...time.Duration) *ClusterHandler {
	budget := 120 * time.Second
	if len(timeout) > 0 && timeout[0] > 0 {
		budget = timeout[0]
	}
	return &ClusterHandler{api.Operations{Service: service}, budget}
}
func (h *ClusterHandler) Status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), api.ReadTimeout)
	defer cancel()
	out, e := h.operations.ClusterStatus(ctx, api.EmptyInput{})
	operationResult(c, out, e)
}
func (h *ClusterHandler) Namespaces(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), api.ReadTimeout)
	defer cancel()
	out, e := h.operations.Namespaces(ctx, api.EmptyInput{})
	operationResult(c, out, e)
}
func (h *ClusterHandler) StorageClasses(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), api.ReadTimeout)
	defer cancel()
	out, e := h.operations.StorageClasses(ctx, api.EmptyInput{})
	operationResult(c, out, e)
}
func (h *ClusterHandler) Resources(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), api.ReadTimeout)
	defer cancel()
	out, e := h.operations.Resources(ctx, api.NamespaceInput{Namespace: c.Param("namespace")})
	operationResult(c, out, e)
}
func (h *ClusterHandler) GatewayResources(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), api.ReadTimeout)
	defer cancel()
	out, e := h.operations.GatewayResources(ctx, api.NamespaceInput{Namespace: c.Param("namespace")})
	operationResult(c, out, e)
}

type ApplyRequest struct {
	api.ApplyInput
	DryRun bool `json:"dryRun,omitempty"`
}

// Apply preserves the legacy HTTP result shape; both paths use the shared use case.
func (h *ClusterHandler) Apply(c *gin.Context) {
	operation(h.writeTimeout, func(ctx context.Context, in ApplyRequest) (any, error) {
		var out api.ApplyOutput
		var e error
		if in.DryRun {
			out, e = h.operations.DryRun(ctx, in.ApplyInput)
		} else {
			out, e = h.operations.Apply(ctx, in.ApplyInput)
		}
		return gin.H{"resources": out.Resources, "result": out.Result}, e
	})(c)
}

type HelmUpgradeRequest = application.HelmUpgradeInput

func (h *ClusterHandler) HelmUpgrade(c *gin.Context) {
	operation(h.writeTimeout, func(ctx context.Context, in HelmUpgradeRequest) (any, error) {
		out, e := h.operations.UpgradeHelm(ctx, in)
		return gin.H{"result": out}, e
	})(c)
}

type NamespaceMetadataRequest = application.NamespaceMetadataChanges

func (h *ClusterHandler) UpdateNamespaceMetadata(c *gin.Context) {
	operation(h.writeTimeout, func(ctx context.Context, in NamespaceMetadataRequest) (any, error) {
		out, e := h.operations.NamespaceMetadata(ctx, api.NamespaceMetadataInput{Namespace: c.Param("namespace"), Labels: in.Labels, Annotations: in.Annotations, RemoveLabels: in.RemoveLabels, RemoveAnnotations: in.RemoveAnnotations})
		return gin.H{"result": out}, e
	})(c)
}
