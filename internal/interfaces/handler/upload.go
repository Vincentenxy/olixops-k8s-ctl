package handler

import (
	"mime"
	"olixops-k8s-ctl/internal/interfaces/httpio"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/pkg/logger"
	"olixops-k8s-ctl/pkg/response"
)

func RegisterPodFileUpload(v1 *gin.RouterGroup, s *application.ClusterService) {
	files := v1.Group("/cluster/pods/files")
	files.POST("/upload", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		kind, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || kind != "application/octet-stream" || c.Request.ContentLength < 0 || c.GetHeader("X-Upload-Id") == "" || c.GetHeader("Content-Range") != "" || c.GetHeader("Range") != "" || c.GetHeader("Content-Encoding") != "" {
			response.AbortWithHTTPStatus(c, 400)
			return
		}
		body := httpio.InterruptibleBody(c.Writer, c.Request)
		defer body.Close()
		out, err := s.ReceivePodFile(c.Request.Context(), c.GetHeader("X-Upload-Id"), body, c.Request.ContentLength)
		logger.Info(c.Request.Context(), "pod file upload", zap.Int64("bytes", out.Size), zap.Bool("success", err == nil))
		if err != nil {
			operationResult(c, nil, err)
			return
		}
		response.Success(c, out)
	})
}
