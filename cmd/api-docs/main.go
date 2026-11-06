// api-docs generates the HTTP contract from registered SDK tool schemas and the
// tested correspondence table. It never loads kubeconfig or contacts Kubernetes.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http/httptest"
	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/application/api"
	catalog "olixops-k8s-ctl/internal/application/catalog"
	d "olixops-k8s-ctl/internal/domain/catalog"
	"olixops-k8s-ctl/internal/infrastructure/catalogdb"
	"olixops-k8s-ctl/internal/infrastructure/config"
	mcpiface "olixops-k8s-ctl/internal/interfaces/mcp"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type obj = map[string]any

func schema(v any) obj {
	raw, _ := json.Marshal(v)
	var o obj
	_ = json.Unmarshal(raw, &o)
	if o == nil {
		o = obj{}
	}
	return o
}
func envelope(data any) obj {
	return obj{"type": "object", "required": []string{"code", "msg", "data"}, "properties": obj{"code": obj{"type": "integer"}, "msg": obj{"type": "string"}, "data": data}}
}
func response(data any) obj {
	return obj{"description": "Operation response", "content": obj{"application/json": obj{"schema": envelope(data)}}}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("out", "docs", "output directory")
	flag.Parse()
	tmp, err := os.MkdirTemp("", "k8s-ctl-api-docs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	store, err := catalogdb.Open(filepath.Join(tmp, "catalog.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cat, err := catalog.New(ctx, store, nil, d.Gateway{ID: "doc", Cluster: "doc", Namespace: "demo", Name: "gateway", Service: "gateway", PortStart: 30000, PortEnd: 30000}, nil)
	if err != nil {
		return err
	}
	svc := application.NewClusterService(nil, nil, config.KubernetesConfig{})
	server := httptest.NewServer(mcpiface.NewHandler(svc, "contract", mcpiface.Options{Catalog: cat, WriteEnabled: true, ShellEnabled: true, PodExecEnabled: true}))
	defer server.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "api-docs", Version: "1"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		return err
	}
	defer session.Close()
	tools := map[string]*sdk.Tool{}
	cursor := ""
	for {
		list, e := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if e != nil {
			return e
		}
		for _, tool := range list.Tools {
			tools[tool.Name] = tool
		}
		cursor = list.NextCursor
		if cursor == "" {
			break
		}
	}
	paths := obj{}
	matrix := "# HTTP / MCP 操作覆盖矩阵\n\n由 `go run ./cmd/api-docs` 从实际 MCP schema 与路由对照生成。HTTP 的入口开关为 `http.enabled`；MCP 工具由其入口及写开关控制。业务范围、grants、写开关和拦截器始终在应用层生效。Catalog 需启用；Shell/终端/文件需显式启用相关能力。\n\n输入/输出字段、必填项见 [OpenAPI](http-openapi.json)。查询预算 30 秒，写操作使用 command-timeout-seconds（缺省 120 秒），Shell/终端准备预算 35 秒；文件字节流不套普通操作总超时。\n\n| MCP 工具 | HTTP | 应用方法 | 属性 | 输入/输出契约 | 能力开关 | 测试入口 |\n| --- | --- | --- | --- | --- | --- | --- |\n"
	for _, route := range api.Routes {
		tool, ok := tools[route.Tool]
		if !ok {
			return fmt.Errorf("mapped tool missing: %s", route.Tool)
		}
		delete(tools, route.Tool)
		in, out := schema(tool.InputSchema), schema(tool.OutputSchema)
		if route.Tool == "apply_manifest" {
			props := schema(in["properties"])
			props["dryRun"] = obj{"type": "boolean", "default": false}
			in["properties"] = props
			props = schema(out["properties"])
			delete(props, "dryRun")
			out["properties"] = props
			out["required"] = []string{"resources", "result"}
		}
		if route.Tool == "helm_upgrade" {
			out = obj{"type": "object", "properties": obj{"result": out}, "required": []string{"result"}}
		}
		responses := obj{"200": response(out)}
		for _, code := range []string{"400", "401", "403", "404", "409", "408", "502", "503", "504"} {
			responses[code] = obj{"$ref": "#/components/responses/OperationError"}
		}
		op := obj{"operationId": route.Tool, "summary": route.Tool, "description": tool.Description, "x-application-method": route.Application, "responses": responses}
		if route.Method == "POST" {
			op["requestBody"] = obj{"required": true, "content": obj{"application/json": obj{"schema": in}}}
		}
		paths[route.Path] = obj{strings.ToLower(route.Method): op}
		attr := "read"
		if tool.Annotations != nil && !tool.Annotations.ReadOnlyHint {
			attr = "write"
		}
		gates := "HTTP: http.enabled; MCP: mcp.enabled"
		if attr == "write" {
			gates += "; MCP: mcp.write-enabled"
		}
		tests := "TestAllToolsHaveHTTPRoutes; TestHTTPOperationsEndToEnd / TestMCPOperationsEndToEnd（同组）"
		if !strings.HasPrefix(route.Path, "/api/v1/cluster/") {
			gates += "; catalog.enabled"
			tests = "TestAllToolsHaveHTTPRoutes; TestCatalogHTTPMCPParityAndWriteDiscovery（同组）"
		} else if attr == "write" {
			gates += "; kubernetes.write-enabled"
		}
		if strings.Contains(route.Path, "/pods/") && route.Tool != "get_pod_logs" {
			gates += "; pod-exec.enabled; shell.enabled（exec 仅 shell action 要求）"
			tests = "TestAllToolsHaveHTTPRoutes; TestOperatorHTTPWithoutMainMCP; TestTerminalAndBinaryDownloadThroughMainMCP / TestUploadThroughMainMCP（同组）"
		}
		matrix += fmt.Sprintf("| %s | %s %s | %s | %s | [request / data: %s](http-openapi.json) | %s | %s |\n", route.Tool, route.Method, route.Path, route.Application, attr, route.Tool, gates, tests)
	}
	if len(tools) > 0 {
		return fmt.Errorf("%d SDK tools lack HTTP mappings", len(tools))
	}
	// Existing legacy aliases remain documented, sharing the exact operation schema.
	alias := func(path, method, source, sourceMethod string, parameters any) {
		raw, _ := json.Marshal(paths[source].(obj)[sourceMethod])
		var op obj
		_ = json.Unmarshal(raw, &op)
		op["operationId"] = strings.ReplaceAll(strings.Trim(path, "/"), "/", "_") + "_" + method
		op["deprecated"] = false
		if parameters != nil {
			op["parameters"] = parameters
		}
		if method == "get" {
			delete(op, "requestBody")
		}
		paths[path] = obj{method: op}
	}
	nsParam := []any{obj{"name": "namespace", "in": "path", "required": true, "schema": obj{"type": "string"}}}
	alias("/api/v1/cluster/namespaces/{namespace}/resources", "get", "/api/v1/cluster/resources/summary", "post", nsParam)
	alias("/api/v1/cluster/namespaces/{namespace}/gateway-resources", "get", "/api/v1/cluster/gateways/list", "post", nsParam)
	alias("/api/v1/cluster/namespaces/{namespace}/metadata", "post", "/api/v1/cluster/namespaces/metadata", "post", nsParam)
	legacy := paths["/api/v1/cluster/namespaces/{namespace}/metadata"].(obj)["post"].(obj)
	body := legacy["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	delete(body["properties"].(map[string]any), "namespace")
	body["required"] = []string{}
	legacyResponse := legacy["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)
	legacyResponse["data"] = obj{"type": "object", "properties": obj{"result": legacyResponse["data"]}}
	alias("/api/v1/services", "get", "/api/v1/services/list", "post", nil)
	alias("/api/v1/ports", "get", "/api/v1/ports/list", "post", nil)
	paths["/api/v1/operator/capabilities"] = obj{"get": obj{"operationId": "operator_capabilities", "responses": obj{"200": response(obj{"type": "object", "properties": obj{"clusterId": obj{"type": "string"}, "runtimeId": obj{"type": "string"}, "apiVersion": obj{"type": "string"}, "version": obj{"type": "string"}, "http": obj{"type": "boolean"}, "mcp": obj{"type": "boolean"}, "kubernetesWrites": obj{"type": "boolean"}, "mcpWrites": obj{"type": "boolean"}, "podExec": obj{"type": "boolean"}, "terminal": obj{"type": "boolean"}, "files": obj{"type": "boolean"}, "catalog": obj{"type": "boolean"}}})}}}
	paths[application.DownloadRoute] = obj{"post": obj{"operationId": "transfer_download", "description": "Single-use authenticated download. Errors after headers abort the stream. Range unsupported.", "requestBody": obj{"required": true, "content": obj{"application/json": obj{"schema": obj{"type": "object", "required": []string{"downloadId"}, "properties": obj{"downloadId": obj{"type": "string"}}}}}}, "responses": obj{"200": obj{"description": "File stream", "content": obj{"application/octet-stream": obj{"schema": obj{"type": "string", "format": "binary"}}}}, "400": response(obj{}), "404": response(obj{})}}}
	paths[application.UploadRoute] = obj{"post": obj{"operationId": "transfer_upload", "description": "Requires Content-Length; chunked and resume unsupported. One new file, never overwrite. No application size cap.", "parameters": []any{obj{"name": "X-Upload-Id", "in": "header", "required": true, "schema": obj{"type": "string"}}, obj{"name": "Content-Length", "in": "header", "required": true, "schema": obj{"type": "integer", "minimum": 0}}}, "requestBody": obj{"required": true, "content": obj{"application/octet-stream": obj{"schema": obj{"type": "string", "format": "binary"}}}}, "responses": obj{"200": response(obj{"type": "object", "properties": obj{"path": obj{"type": "string"}, "size": obj{"type": "integer"}, "sha256": obj{"type": "string"}, "podUID": obj{"type": "string"}}}), "400": response(obj{}), "404": response(obj{}), "502": response(obj{}), "503": response(obj{})}}}
	doc := obj{"openapi": "3.1.0", "info": obj{"title": "k8s-ctl operator HTTP API", "version": "v1", "description": "Business operations mapped to MCP, plus file streams and capabilities. Authentication management and health endpoints are documented separately in authentication.md."}, "servers": []any{obj{"url": "http://127.0.0.1:8091"}}, "security": []any{obj{"bearerAuth": []string{}}}, "components": obj{"responses": obj{"OperationError": response(obj{"type": "object", "required": []string{"reason"}, "properties": obj{"reason": obj{"type": "string"}, "result": obj{}}})}, "securitySchemes": obj{"bearerAuth": obj{"type": "http", "scheme": "bearer", "bearerFormat": "JWT / PAT"}}}, "paths": paths}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(*dir, 0755); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(*dir, "http-openapi.json"), append(raw, '\n'), 0644); err != nil {
		return err
	}
	matrix += "\n输入/输出契约列按 OpenAPI operationId 定位 requestBody 与 responses.200 的 data；GET 无请求体。能力开关列列出入口前提，实际业务写入校验、受限诊断配置与范围检查以应用方法为准。测试列的“同组”表示该能力族的集成测试入口，不代表每个工具都做过真实写入。\n\n所有映射均由 TestAllToolsHaveHTTPRoutes 验证存在，资源双入口语义由 TestHTTPMCPInputAndResultParity、Catalog 对照测试覆盖；终端/文件操作器链路见 TestOperatorHTTPWithoutMainMCP。路径存在不等于每个写操作都经过真实集群验收。错误原因使用共享模型，文件成功响应为二进制流。\n"
	return os.WriteFile(filepath.Join(*dir, "http-mcp-coverage.md"), []byte(matrix), 0644)
}
