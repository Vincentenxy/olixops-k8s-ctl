package handler

import (
	"io"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/domain"
	"olixops-k8s-ctl/pkg/logger"
	"olixops-k8s-ctl/pkg/response"
)

func RegisterPodFileDownload(v1 *gin.RouterGroup, s *application.ClusterService) {
	files := v1.Group("/cluster/pods/files")
	files.POST("/download", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if c.GetHeader("Range") != "" {
			response.AbortWithHTTPStatus(c, 400)
			return
		}
		var in domain.DownloadRequest
		if !bindSmallJSON(c, &in) {
			return
		}
		file, err := s.OpenPodFile(c.Request.Context(), in.DownloadID)
		if err != nil {
			operationResult(c, nil, err)
			return
		}
		defer file.Close()
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
		c.Header("X-Content-Type-Options", "nosniff")
		// Chunked response is intentionally not completed until tar and exec finish.
		// If a late failure occurs, abort HTTP rather than append JSON or end a corrupt success.
		c.Status(200)
		c.Writer.Flush()
		n, err := io.CopyN(c.Writer, file.Reader, file.Size)
		if err == nil {
			err = file.Finish()
		}
		logger.Info(c.Request.Context(), "pod file download", zap.Int64("bytes", n), zap.Bool("success", err == nil))
		if err != nil {
			panic(http.ErrAbortHandler)
		}
	})
}
