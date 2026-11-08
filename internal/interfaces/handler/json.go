package handler

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"olixops-k8s-ctl/internal/interfaces/httpio"
	"olixops-k8s-ctl/pkg/response"
)

// bindSmallJSON bounds diagnostic and identity request bodies and rejects unknown fields or extra JSON values.
func bindSmallJSON(c *gin.Context, in any) bool {
	raw, err := httpio.ReadJSONBody(c.Writer, c.Request, 8192)
	if err != nil {
		operationResult(c, nil, err)
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
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
