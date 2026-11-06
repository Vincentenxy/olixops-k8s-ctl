# HTTP 操作器与双入口使用说明

当前版本已提供 HTTP/MCP 双入口：57 个主 MCP 业务工具（全部能力开启时）均有 HTTP 对应入口。HTTP 和 MCP 共用类型化应用契约及业务实现；HTTP 采用与 MCP SDK 相同的 JSON Schema 推导/校验库，业务默认值和授权仍由应用服务处理。

完整路径与应用方法见 [覆盖矩阵](http-mcp-coverage.md)，请求/响应 schema 见 [OpenAPI 3.1](http-openapi.json)。旧路径和成功响应保持兼容；错误现在区分参数、拒绝、冲突、找不到和上游故障。此文档描述下层操作器；统一多集群平台尚未实现。

## 启动与入口开关

```bash
go run ./cmd --config-dir ./configs
```

配置示例：[独立使用](../configs/config.standalone.example.yaml)、[上层操作器](../configs/config.operator.example.yaml)。复制所选示例为部署目录中的 config.yaml，按实际环境填写公钥挂载路径、issuer/audience、Namespace 与 grants。示例不含真实凭据，填写后才可运行。

| 配置 | 含义 |
| --- | --- |
| http.enabled | 普通业务 HTTP 入口，默认 true；不控制健康、认证管理以及 MCP 所需文件数据流 |
| mcp.enabled | 主 MCP 入口；操作器模式可 false |
| mcp.write-enabled | 主 MCP 写工具是否注册，不再作为 HTTP 执行的前提 |
| kubernetes.write-enabled | Kubernetes 写操作的应用层开关；不把 SQLite 配置/端口记录写入等同 Kubernetes 写入 |
| pod-exec.enabled / shell.enabled | 执行与任意 Shell/终端/文件能力显式启用；保留 Namespace、grants、Pod UID、RBAC 和拦截器检查 |
| mcp.diagnostics-only | 旧受限诊断配置，保留兼容；完整操作器使用 false |
| cluster.id | 稳定集群标识；省略时沿用 catalog.cluster，默认 configured。二者显式配置时必须一致 |
| server.public-url | 本实例可访问地址，可包含 /k8sctl 前缀；平台应重写为自己的文件传输地址 |

关闭普通 HTTP、只连接主 MCP 时，文件工具依赖的上传下载数据流路由仍然可用。主 MCP 与内部 Kubernetes MCP 执行 sidecar/managed 是两项不同能力：关闭主 MCP 不会移除单次 exec 的内部执行依赖。

认证要求沿用现有规则：推荐 JWT/PAT；旧 API key 模式兼容原有 HTTP 使用方。操作器的个人身份必须来自已验证的 JWT，不能只传 X-User-Id。上层个人 PAT 不会自动被独立下层 PAT 数据库认可；上层可验证 PAT 后签发下层信任的短期 JWT。下层不提供令牌交换服务。

## 查询与资源操作

以下 curl 用环境变量代指调用者已持有的令牌，不要求服务端使用环境变量配置。不要把真实 token 写入文档、仓库或 URL。

```bash
# Base 可为 http://127.0.0.1:8091，或 https://operator.example/k8sctl。
curl --fail-with-body "$K8S_CTL_BASE/api/v1/operator/capabilities" \
  -H "Authorization: Bearer $K8S_CTL_PAT"

curl --fail-with-body "$K8S_CTL_BASE/api/v1/cluster/namespaces" \
  -H "Authorization: Bearer $K8S_CTL_PAT"

curl --fail-with-body "$K8S_CTL_BASE/api/v1/cluster/resources/yaml" \
  -H "Authorization: Bearer $K8S_CTL_PAT" -H 'Content-Type: application/json' \
  --data '{"namespace":"demo","kind":"Deployment","name":"app"}'

curl --fail-with-body "$K8S_CTL_BASE/api/v1/cluster/workloads/scale" \
  -H "Authorization: Bearer $K8S_CTL_PAT" -H 'Content-Type: application/json' \
  --data '{"namespace":"demo","kind":"Deployment","name":"app","replicas":0}'
```

普通成功返回 `{"code":0,"msg":"success","data":...}`。能力查询包含 clusterId、runtimeId、version、apiVersion、HTTP/MCP、Kubernetes 写、exec、terminal、files 和 Catalog 状态。上层注册实例时核对 clusterId；runtimeId 每次启动不同，用于识别旧会话引用已失效。不返回 kubeconfig、凭据或集群连接串。

参数业务请求只使用 POST；已有路径参数 GET 保留兼容。新增 `/cluster/resources/summary` 与 `/cluster/gateways/list` 接收 namespace，分别对应原 Namespace 子路径查询。

## 命令与持续终端

HTTP 单次命令仍使用 `POST /api/v1/cluster/pods/exec`：

```json
{"namespace":"demo","pod":"app","container":"app","action":"shell","command":"pwd; id","timeoutSeconds":10}
```

HTTP 与 MCP 共享 64 KiB Shell 命令限制，HTTP 不再受旧 8 KiB 诊断 JSON 限制。命令在目标容器执行，不在控制服务本机运行。超时不保证远端子进程停止，也不能自动重试。

| 终端操作 | HTTP POST 路径 | JSON 示例 |
| --- | --- | --- |
| 打开 | /api/v1/cluster/pods/terminals/open | `{"namespace":"demo","pod":"app","container":"app","tty":true}` |
| 输入 | /api/v1/cluster/pods/terminals/write | `{"sessionId":"<返回的ID>","input":"pwd\n"}` |
| 读取 | /api/v1/cluster/pods/terminals/read | `{"sessionId":"<返回的ID>","cursor":0,"waitSeconds":1}` |
| 关闭 | /api/v1/cluster/pods/terminals/close | `{"sessionId":"<返回的ID>"}` |

开启 TTY 后可写 `"input":"\u0003"` 发送 Ctrl+C。读取返回 outputBase64、nextCursor、lostBytes 和 state；按 nextCursor 继续读，识别输出被覆盖的情况。会话独立于 open 请求存在，受用户归属、生命周期与空闲超时约束；不是 SSH，也不因增加 HTTP 就成为可跨实例共享的会话。

## 上传和下载

准备接口统一接收 `namespace/pod/container/path`：

- `POST /api/v1/cluster/pods/files/prepare-upload` → uploadId、uploadUrl、method、expiresAt、podUID。
- `POST /api/v1/cluster/pods/files/prepare-download` → downloadId、downloadUrl、method、expiresAt、podUID。

引用短期、单次、用户与 Pod UID 绑定。准备引用不传文件字节。用准备返回的 URL 执行传输，传输阶段再次校验身份与目标。

```bash
# uploadUrl/uploadId 来自准备上传接口；URL 应来自你信任的实例或平台。
curl --fail-with-body "$UPLOAD_URL" \
  -H "Authorization: Bearer $K8S_CTL_PAT" \
  -H "X-Upload-Id: $UPLOAD_ID" -H 'Content-Type: application/octet-stream' \
  --data-binary @./local-file.bin

# downloadUrl/downloadId 来自准备下载接口；不把二进制内容放进模型上下文。
curl --fail-with-body "$DOWNLOAD_URL" \
  -H "Authorization: Bearer $K8S_CTL_PAT" -H 'Content-Type: application/json' \
  --data "{\"downloadId\":\"$DOWNLOAD_ID\"}" --output ./download.bin
```

上传必须带 Content-Length，普通文件的 curl --data-binary @file 会设置它；不支持未知长度 chunked、覆盖已有目标或续传。完整上传并核对远端 SHA-256 后才发布文件。下载成功为二进制流，失败响应开始前是 JSON，响应开始后失败会中断流；调用者必须检查 curl 退出状态，不能把留下的部分文件作为成功结果。

上层流式转发时保持上传 Content-Length、背压、取消和断流语义，用有限缓冲，不整体读入内存。上层为自己的 transferId/sessionId 保存用户、clusterId、backendId、runtimeId、下层引用及有效期，再重新生成平台地址。当前项目没有实现上层代理，模拟代理和真实下层分别验收。

不设置应用固定文件大小上限，不代表 Higress 没有限制；并发、大文件、断点续传和多副本状态仍见待办。

## 错误和兼容

应用错误保留统一结构，例如：

```json
{"code":403,"msg":"kubernetes write operations are disabled","data":{"reason":"feature_disabled","result":{}}}
```

常见 reason：invalid_argument、unauthenticated、forbidden、not_found、conflict、feature_disabled、upstream_unavailable、deadline_exceeded、canceled、execution_unknown。Shell 拦截器保留其专用原因码，Catalog 保留已有业务原因码。认证失败、路由不存在等前置中间件错误也提供 data.reason。不得把底层原始报错或凭据返回给调用方。

未找到或跨用户的会话/文件引用返回 404；Pod 被替换等冲突返回 409；拒绝/功能关闭返回 403。部分旧接口过去统一返回 400，客户端应改为检查 HTTP 状态与 data.reason。命令失败时 data.result 保留执行状态；MCP 对应 isError 和同一原因说明。不要仅按 HTTP 200 或 Go err=nil 判断执行成功。

普通 JSON 请求上限 4 MiB，另有各业务字段限制。MCP JSON-RPC 外层也有 4 MiB 上限；接近上限的 JSON 应为协议封装预留空间。查询预算 30 秒，写操作使用 command-timeout-seconds（默认 120 秒），exec/终端准备 35 秒；终端 read 最多等待 5 秒。文件字节流不使用普通 JSON 大小限制或统一短总超时。

## 文档与验收

执行 `go run ./cmd/api-docs` 可重建覆盖矩阵和 OpenAPI，命令只使用临时 SQLite 与本机协议端点，不加载 kubeconfig、不访问 Kubernetes。57 个工具与 HTTP 实际路由的对应由测试核对，新增工具必须同步对照表。

验证范围、真实集群操作、清理结果及限制见 [双入口测试记录](testing/http-operator-2026-11-06.md)。
