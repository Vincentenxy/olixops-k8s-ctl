package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/pkg/response"
)

type ClusterHandler struct {
	service *application.ClusterService
}

func NewClusterHandler(service *application.ClusterService) *ClusterHandler {
	return &ClusterHandler{service: service}
}

func (h *ClusterHandler) Status(c *gin.Context) {
	data, err := h.service.Status(c.Request.Context())
	if err != nil {
		h.internalError(c, err)
		return
	}
	response.Success(c, data)
}

func (h *ClusterHandler) Namespaces(c *gin.Context) {
	data, err := h.service.ListNamespaces(c.Request.Context())
	if err != nil {
		h.internalError(c, err)
		return
	}
	response.Success(c, gin.H{"items": data})
}

func (h *ClusterHandler) StorageClasses(c *gin.Context) {
	data, err := h.service.ListStorageClasses(c.Request.Context())
	if err != nil {
		h.internalError(c, err)
		return
	}
	response.Success(c, gin.H{"items": data})
}

func (h *ClusterHandler) Resources(c *gin.Context) {
	data, err := h.service.ListResources(c.Request.Context(), c.Param("namespace"))
	if err != nil {
		h.clientError(c, err)
		return
	}
	response.Success(c, data)
}

func (h *ClusterHandler) GatewayResources(c *gin.Context) {
	data, err := h.service.ListGatewayResources(c.Request.Context(), c.Param("namespace"))
	if err != nil {
		h.clientError(c, err)
		return
	}
	response.Success(c, data)
}

type ApplyRequest struct {
	Namespace  string `json:"namespace" binding:"required"`
	Manifest   string `json:"manifest" binding:"required"`
	ServerSide bool   `json:"serverSide"`
	DryRun     bool   `json:"dryRun"`
}

func (h *ClusterHandler) Apply(c *gin.Context) {
	var request ApplyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.AbortWithHTTPStatus(c, 400)
		return
	}
	result, resources, err := h.service.ApplyManifest(c.Request.Context(), request.Manifest, request.Namespace, request.ServerSide, request.DryRun)
	if err != nil {
		h.clientError(c, err)
		return
	}
	response.Success(c, gin.H{"resources": resources, "result": result})
}

type HelmUpgradeRequest struct {
	Release    string `json:"release" binding:"required"`
	Chart      string `json:"chart" binding:"required"`
	Namespace  string `json:"namespace" binding:"required"`
	ValuesYAML string `json:"valuesYaml"`
	Wait       bool   `json:"wait"`
}

type NamespaceMetadataRequest struct {
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	RemoveLabels      []string          `json:"removeLabels,omitempty"`
	RemoveAnnotations []string          `json:"removeAnnotations,omitempty"`
}

func (h *ClusterHandler) UpdateNamespaceMetadata(c *gin.Context) {
	var request NamespaceMetadataRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		response.AbortWithHTTPStatus(c, http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		response.AbortWithHTTPStatus(c, http.StatusBadRequest)
		return
	}
	result, err := h.service.UpdateNamespaceMetadata(c.Request.Context(), c.Param("namespace"), application.NamespaceMetadataChanges{
		Labels:            request.Labels,
		Annotations:       request.Annotations,
		RemoveLabels:      request.RemoveLabels,
		RemoveAnnotations: request.RemoveAnnotations,
	})
	if err != nil {
		h.clientError(c, err)
		return
	}
	response.Success(c, gin.H{"result": result})
}

func (h *ClusterHandler) HelmUpgrade(c *gin.Context) {
	var request HelmUpgradeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.AbortWithHTTPStatus(c, 400)
		return
	}
	result, err := h.service.HelmUpgrade(c.Request.Context(), request.Release, request.Chart, request.Namespace, request.ValuesYAML, request.Wait)
	if err != nil {
		h.clientError(c, err)
		return
	}
	response.Success(c, gin.H{"result": result})
}

func (h *ClusterHandler) clientError(c *gin.Context, err error) {
	_ = err
	response.AbortWithHTTPStatus(c, http.StatusBadRequest)
}

func (h *ClusterHandler) internalError(c *gin.Context, err error) {
	_ = err
	response.AbortWithHTTPStatus(c, http.StatusServiceUnavailable)
}
