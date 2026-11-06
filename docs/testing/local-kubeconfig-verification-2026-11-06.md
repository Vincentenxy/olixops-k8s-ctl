# 本地 kubeconfig 真实集群验证报告

测试时间：2026-11-06T11:49:47+08:00。本轮针对当前工作区代码重新构建主程序，在本机启动服务后，通过 HTTP 和官方 Go MCP SDK 调用真实集群。

## 结果

真实环境检查共 48 项，全部通过：HTTP/MCP 及内容断言 47 项，本地 exec 启动边界检查 1 项。另已通过不使用缓存的全量 go test、go vet 和 git diff --check；修复后的未知路由、PAT/MCP exec 集成和执行预算用例也通过 go test -race。

## 运行环境

- 服务地址：http://127.0.0.1:18091；测试后保留运行。原有 8091 进程未重启或修改。
- kubeconfig：默认 ~/.kube/config；当前 context 为 poc-cluster；服务端 Kubernetes v1.28.0。未输出 kubeconfig、令牌、证书或连接串。
- 本次启用 PAT 认证，独立生成测试密钥，使用隔离的 auth.db 与 catalog.db。所有本地数据位于 Git 忽略的 .local/live-verification 下。
- Kubernetes 写开关关闭，未创建、更新或删除集群资源，未拉取或构建容器镜像。
- 端口同步只读取真实 Higress 资源，并写入本次隔离的本地台账。
- 使用现有 olixops-k8s-ctl Pod/controller 容器读取 5 行日志。日志与资源 YAML 原文未写入本报告。

## 本轮发现及修复

真实检查发现未匹配的路由仍返回 Gin 默认纯文本 404，与统一 JSON 响应规范不符。已为 Router 增加 NoRoute 处理，返回 code/msg/data 结构并保留 X-Request-Id；增加缺失路由回归测试。重建并仅重启 18091 测试实例后，重新执行本报告全部检查，错误响应同时检查状态码和 JSON envelope。

## 关键证据

- MCP 握手和工具发现成功，共 50 个工具；exec 未开启，因此不包含 exec_pod_command，未暴露上游 pods_exec。
- HTTP 和 MCP 均读到真实 Pod 日志，各返回 1447 字节；未截断。
- 成功读取 wx-test-ns-001 的原始 Namespace YAML；Secret YAML 请求被拒绝。
- Helm 列表、Deployment 状态、资源列表、事件和标准服务目录查询成功。
- 无效/缺失 PAT 返回 401；测试 PAT 撤销后，HTTP 和已经建立的 MCP 客户端后续请求均被拒绝。
- 30000–30200 共 201 个端口。同步结果：external=11、free=190、exposed=11。free 是本次资源快照下的台账状态，不证明端口已暴露或网络可达。

## 检查明细

| 检查 | 结果 | 摘要 |
| --- | --- | --- |
| HTTP health | PASS | {"httpStatus":200,"requestIdEcho":true} |
| HTTP readiness | PASS | {"httpStatus":200,"requestIdEcho":true} |
| Missing PAT rejected | PASS | {"httpStatus":401,"requestIdEcho":true} |
| Invalid PAT rejected | PASS | {"httpStatus":401,"requestIdEcho":true} |
| PAT identity | PASS | {"httpStatus":200,"requestIdEcho":true} |
| PAT identity owner | PASS | {} |
| HTTP real cluster status | PASS | {"gitVersion":"v1.28.0","namespaceCount":5,"serverVersion":"v1.28.0"} |
| HTTP scoped namespaces | PASS | {"httpStatus":200,"requestIdEcho":true} |
| Namespace count | PASS | {"count":5} |
| HTTP resources | PASS | {"httpStatus":200,"requestIdEcho":true} |
| HTTP storage classes | PASS | {"httpStatus":200,"requestIdEcho":true} |
| Out of scope namespace rejected | PASS | {"httpStatus":400,"requestIdEcho":true} |
| HTTP real pod logs | PASS | {"httpStatus":200,"requestIdEcho":true} |
| HTTP logs content | PASS | {"bytes":1447,"truncated":false} |
| Unknown log fields rejected | PASS | {"httpStatus":400,"requestIdEcho":true} |
| Raw exec route disabled | PASS | {"httpStatus":404,"requestIdEcho":true} |
| Old public MCP route removed | PASS | {"httpStatus":404,"requestIdEcho":true} |
| MCP requires PAT | PASS | {"httpStatus":401,"requestIdEcho":true} |
| REST cluster writes disabled | PASS | {"httpStatus":400,"requestIdEcho":true} |
| HTTP standard services | PASS | {"httpStatus":200,"requestIdEcho":true} |
| HTTP port pool | PASS | {"httpStatus":200,"requestIdEcho":true} |
| Port pool covers 201 ports | PASS | {"count":201} |
| MCP SDK initialization | PASS | {} |
| MCP tool discovery | PASS | {"count":50,"execAbsent":true} |
| MCP cluster_status | PASS | {"isError":false} |
| MCP list_namespaces | PASS | {"isError":false} |
| MCP list_resources | PASS | {"isError":false} |
| MCP list_gateway_resources | PASS | {"isError":false} |
| MCP get_rollout_status | PASS | {"isError":false} |
| MCP get_resource | PASS | {"isError":false} |
| MCP get_resource_yaml | PASS | {"isError":false,"yamlBytes":616} |
| MCP list_resource_objects | PASS | {"isError":false} |
| MCP list_events | PASS | {"isError":false} |
| MCP get_pod_logs | PASS | {"isError":false,"logBytes":1447} |
| MCP list_helm_releases | PASS | {"isError":false} |
| MCP list_standard_services | PASS | {"isError":false} |
| MCP list_service_templates | PASS | {"isError":false} |
| MCP get_service_template | PASS | {"isError":false} |
| MCP sync_gateway_ports | PASS | {"isError":false} |
| MCP list_gateway_ports | PASS | {"isError":false} |
| Live Higress port inventory | PASS | {"count":201,"exposed":11,"states":{"external":11,"free":190}} |
| MCP get_resource_yaml | PASS | {"isError":true,"yamlBytes":0} |
| MCP list_resources | PASS | {"isError":true} |
| MCP get_pod_logs | PASS | {"isError":true,"logBytes":0} |
| PAT self revoke | PASS | {"httpStatus":200,"requestIdEcho":true} |
| Revoked PAT rejected by HTTP | PASS | {"httpStatus":401,"requestIdEcho":true} |
| Revoked PAT rejected by existing MCP client | PASS | {} |
| 本地 kubeconfig 开启 sidecar exec 被拒绝 | PASS | 启动退出码 1，要求 kubernetes.in-cluster |

## 未覆盖的真实环境场景

- 本次没有在目标集群部署 Kubernetes MCP Server sidecar，也未运行真实 Pod exec。该模式强制 in-cluster，本地 kubeconfig 不能绕过此边界。上游调用、超时和错误处理由本地模拟 SDK/Kubernetes API 自动测试覆盖；真实 exec 仍需同 Pod 部署后验收。
- 没有接入公司真实 JWT/SSO；JWT 验签等逻辑由自动测试验证，真实 HTTP/MCP 接入使用本次独立 PAT。
- 没有执行实际安装、扩缩容、重启、删除或数据库服务外部协议连接测试。
- 本报告中的 48 个检查不代表逐个执行所有 50 个工具。

## 测试后状态

- 测试服务仍监听 127.0.0.1:18091；集群写入保持关闭。
- 本轮测试 PAT 已撤销，未向报告或终端输出令牌。继续调用认证接口需通过管理员 CLI 创建新的个人令牌。
- 代码及报告未提交 Git。
