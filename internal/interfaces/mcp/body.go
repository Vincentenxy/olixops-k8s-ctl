package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"olixops-k8s-ctl/internal/application/api"
	"olixops-k8s-ctl/internal/interfaces/httpio"
)

// Bound only MCP's incoming JSON body, not the lifetime of SDK tool execution.
// Successful requests remain untouched JSON-RPC for the SDK to decode/validate.
func boundJSONBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, err := httpio.ReadJSONBody(w, r, api.MaxJSONBytes)
			if err != nil {
				failure := api.Classify(err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(failure.Status)
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32600, "message": failure.Message, "data": map[string]any{"reason": failure.Reason}}})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		next.ServeHTTP(w, r)
	})
}
