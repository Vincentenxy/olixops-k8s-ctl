package handler

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"olixops-k8s-ctl/pkg/response"
)

// bindSmallJSON bounds diagnostic and identity request bodies and rejects unknown fields or extra JSON values.
func bindSmallJSON(c *gin.Context, in any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(in); err != nil {
		response.AbortWithHTTPStatus(c, 400)
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		response.AbortWithHTTPStatus(c, 400)
		return false
	}
	return true
}
