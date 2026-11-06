# HTTP / MCP 双入口验收记录（2026-11-06）

本轮完成单集群 k8s-ctl 操作器：认证 HTTP 与主 MCP 共用应用契约、校验和执行服务。全部能力开启时，57 个业务 MCP 工具均有 HTTP 对应入口。上层多集群平台未实现，本轮以模拟代理验证接入契约，以真实集群验证下层操作链路。

## 自动化检查

以下命令均通过，未提交或推送 Git；保留工作区原有修改。

```bash
go test ./... -count=1
go vet ./...
go test -race ./internal/application/api ./internal/application/shell ./internal/application/shell/interceptor ./tests -count=1
go run ./cmd/api-docs
git diff --check
```

变更的 Go 文件已执行 gofmt。文档生成器通过实际 SDK 工具发现生成 OpenAPI 与覆盖矩阵，不访问 Kubernetes、不读取 kubeconfig。

| 验证范围 | 证据与结果 |
| --- | --- |
| 双入口契约 | `TestHTTPMCPInputAndResultParity`、`TestDecodePresenceAndClosedObjects`：输出一致；未知字段、大小写字段、null、错误类型和缺失字段被拒绝；显式 0 副本到达业务层；Namespace 非元数据字段拒绝 |
| 全工具对应 | `TestAllToolsHaveHTTPRoutes`：实际 SDK 工具与实际 Gin 路由逐项对应；共 57 个工具；关闭 HTTP 后普通业务路径隐藏，健康、认证与 MCP 文件流保留 |
| 资源、Manifest、工作负载、Helm | `TestHTTPOperationsEndToEnd`、`TestMCPOperationsEndToEnd`：同一能力族在两种协议下对接模拟 Kubernetes/CLI；包括零副本、创建删除、更新、重启回滚、Manifest 与 Helm。HTTP 测试关闭主 MCP |
| Catalog 和端口 | `TestCatalogHTTPMCPParityAndWriteDiscovery` 与现有 Catalog 测试：原路径、输出、发现与写开关回归 |
| 认证及身份 | `TestOperatorHTTPWithoutMainMCP`：可信 JWT；错误 JWT、错误 audience 返回 401/unauthenticated；伪造 X-User-Id 不覆盖 JWT 身份；跨用户终端访问拒绝 |
| 开关及集群标识 | 配置测试：HTTP 独立执行、MCP-only 文件路由、旧 diagnostics-only 约束、cluster.id 默认兼容与显式冲突拒绝 |
| 执行结果与错误 | `TestStableErrorReasons` 与接口测试：拒绝、关闭、未找到、冲突、超时和结果未知区分；保留 Outcome、TimedOut、UpstreamError；认证、路由等前置错误也提供 reason |
| 命令及终端 | HTTP 16 KiB 命令通过，消除旧 8 KiB 限制；会话跨请求持续、用户归属、输入输出、原始控制字符、cursor/lostBytes、过期、关闭/重启失效、Pod UID 更换由既有和新增测试覆盖 |
| 文件传输 | HTTP/MCP 准备与传输；零字节、二进制、长度、SHA-256、单次引用、过期、Pod 替换、截断、只读/写入失败、取消清理、发布结果未知与下载中断回归 |
| 模拟上层代理 | `/k8sctl` 前缀反向代理保留身份、上传 Content-Length 和二进制往返；`TestOperatorProxyDownloadCancellation` 用固定 64 KiB 分块的 64 MiB 合成源证明未全量缓存后再响应，客户端取消可停止下游生产者 |
| 并发检查 | 应用契约、会话、拦截器及整个 tests 包通过 race；不等同几百用户或大文件压力验收 |

逐项路径、应用方法、契约和开关见 [覆盖矩阵](../http-mcp-coverage.md)，完整字段见 [OpenAPI](../http-openapi.json)。矩阵中的能力族测试不代表每一项写操作都在真实集群执行过。

## 真实集群验收

本机使用独立构建的临时服务，HTTP 端口 18096，配置 `http.enabled=true`、`mcp.enabled=false`、`mcp.write-enabled=false`，Catalog 关闭以避免后台业务任务。通过已有有效 PAT 调用认证 HTTP，操作均经 k8s-ctl；没有绕过操作器直接执行本机 kubectl。

测试 Namespace 为 `wx-http-test-e94230c7`。仅在此 Namespace 创建测试 Pod，复用现有节点缓存镜像并显式指定 `imagePullPolicy: Never`。未拉取新镜像、未在容器安装工具、未修改现有应用数据。测试写入使用临时 Pod 的 `/work` emptyDir，根文件系统只读，非 root 运行，不挂载 ServiceAccount token。

以下真实操作全部通过：

1. 创建 Namespace，修改 labels，读取原始 YAML 核对。
2. Manifest validate、Kubernetes server dry-run、apply，等待临时 Pod Running。
3. 资源列表、Pod 日志与单次 Shell 命令。
4. 持续终端 open/write/read/close；在 TTY 中执行 `sleep 30` 后发送 Ctrl+C，后续命令在原 sleep 截止前返回，确认真实中断有效。
5. 上传 2,097,152 字节二进制文件，核对远端 SHA-256；下载并逐字节比较成功；零字节上传成功。
6. 单独在端口 18097 启用主 MCP，SDK 工具发现及真实 Pod YAML 查询成功。该实例 Catalog 关闭，因此工具数为 36；完整配置为 57。

二进制测试文件 SHA-256：

```text
91d3beb88a9b2f778a6c44a1c53b63d3c79931845a9aef84b3fb414610bd1938
```

测试结束通过 HTTP 删除整个 Namespace，再次查询返回 404，确认测试 Pod 与 emptyDir 已清理。两个临时服务均已停止；用户原运行服务未替换。私有测试配置与脚本仅保存在 Git 忽略的 `.local/` 下，报告不包含令牌、证书、kubeconfig 或连接串。

## 边界与后续

- 本轮真实验收覆盖上述下层链路；真实 Helm 安装升级回滚、全部 Catalog 部署写入和所有资源变更未逐一执行，对应自动化使用模拟 Kubernetes/CLI，不能替代这些场景的真实验收。
- 上层平台只使用模拟代理验证，未部署真实平台；多集群选择、集群目录、上层引用映射及 JWT 签发由后续上层项目实现。
- 多副本共享会话、断点续传、未知长度上传、大文件并发和规模压测仍未实现；没有应用固定文件大小上限不代表网关无限制。
- 原成功响应和工具名保持兼容；旧笼统 400 错误现按 401/403/404/409/502/503/504 等语义返回，客户端应检查 `data.reason` 及执行结果，不能自动重试写操作。
- 原始 YAML/日志/文件输出策略保留；未引入自动脱敏或资源 create-user-id/管理员授权。现有 JWT/PAT、grants、范围、RBAC 与 Shell 拦截器继续生效。
- 将本轮源码重新构建并重启实际服务后才会生效。配置与 curl 示例见 [HTTP 操作器使用说明](../http-operator-guide.md)。
