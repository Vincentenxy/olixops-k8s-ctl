# MCP 开发方案与实现记录

更新时间：2026-10-18。

## 当前结果

已接入官方 Go SDK v1.8.0 和 stateless Streamable HTTP。当前有 **15 个只读工具、12 个写工具，共 27 个工具**；默认只注册只读工具，写工具通过配置独立启用。

本轮按用户要求先完成操作能力，**身份认证、角色、权限策略、审批和完整审计尚未实现**。业务层已预留可替换的 `domain.Authorizer`，默认 `DeferredAuthorizer` 明确放行。开关、参数校验、资源白名单和 Namespace 范围检查均不是用户权限系统。

完整权限设计见 [MCP 权限控制设计](mcp-authorization-design.md)，该文档只描述后续方案。

## 实施状态

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| 1 | MCP 协议、发现、JSON Schema 校验、结构化结果、请求 ID 与调用日志 | 已完成 |
| 2 | 原首批 R1～R5、R9、R11 | 已完成 |
| 3 | 其余查询、日志、Helm 和常用写操作 | 已完成，见下方工具清单 |
| 4 | REST/MCP 共用权限校验接口 | 已预留，默认放行；用拒绝适配器测试调用位置 |
| 5 | 认证、策略、审批、完整审计、集群侧最小权限 RBAC | 尚未实现，单独评审 |

## 架构与入口

```text
Gin + X-Request-Id
  ├─ REST /api/v1/cluster/... → 现有 ControlKey 中间件
  └─ MCP  /api/v1/pub/mcp   → 配置启用的工具
                  ↓
       application.ClusterService
       参数/资源/Namespace/总开关检查
       domain.Authorizer（当前为放行占位）
                  ↓
       client-go / kubectl / Helm adapters
                  ↓
          已配置的 Kubernetes 集群
```

- MCP 地址为 `http://127.0.0.1:8091/api/v1/pub/mcp`，不要求 REST 的 `X-Olixops-Control-Key`。启用写工具后，写调用同样没有调用者授权，请勿把已有 REST Key 误认为 MCP 的保护。
- 使用 stateless Streamable HTTP、JSON 响应；无持久会话，无 stdio、旧版独立 SSE 入口。支持版本以 SDK 与客户端协商为准。
- POST 承载 MCP JSON-RPC；GET/DELETE 在当前模式下由 SDK 返回 405，和 Kubernetes adapter 内部使用的 GET/PATCH/PUT/DELETE 无关。
- MCP 使用协议规定的结果结构，不套 REST 的 `code/msg/data`。REST 保留现有响应与 API Key 行为。
- 读工具标注 `readOnlyHint: true`；写工具标注 false，删除、扩缩容、升级等同时标注 destructive。预校验虽不持久化，也按写能力处理。工具标注不能代替服务端权限。
- 请求体最多 4 MiB，manifest/Helm values 各最多 2 MiB，外部命令输出读取过程最多捕获 8 MiB；超出则失败，不能静默截断结构化 JSON。
- 读工具最多执行 30 秒；写工具总时限使用 `kubernetes.command-timeout-seconds`（默认 120 秒）。Kubernetes HTTP 单次请求也有 30 秒上限。客户端可以取消请求，CLI 子进程会被终止；取消/超时不表示已发生的集群变更自动回滚。
- 使用跨站保护与 SDK 默认回环 Host 保护，不开放跨域浏览器调用。
- 复用 `pkg/logger`，透传或生成 `X-Request-Id` 并回写。调用日志包含工具名、读写属性、耗时、成功与否，不记录参数、YAML、完整输出或上游原始错误。
- 上游错误按通用消息返回，避免错误包含连接地址、凭据、YAML；工具失败用 `isError: true`，不能只判断 HTTP 200。

## 配置与运行

```yaml
mcp:
  enabled: true
  write-enabled: false
kubernetes:
  write-enabled: true
  allowed-namespaces: []
  command-timeout-seconds: 120
```

开关在启动时读取。环境变量示例：

```bash
# 仅启动读取能力（默认）
go run ./cmd

# 开放 MCP 写工具；同时需要 Kubernetes 写总开关
MCP_WRITE_ENABLED=true KUBERNETES_WRITE_ENABLED=true go run ./cmd

# 完全关闭 MCP
MCP_ENABLED=false go run ./cmd
```

| mcp.write-enabled | kubernetes.write-enabled | 行为 |
| --- | --- | --- |
| false | 任意 | 仅注册 15 个读工具，写工具不存在 |
| true | false | 注册 27 个工具，但写调用被业务层拒绝 |
| true | true | 27 个工具可调用，仍受固定集群、Namespace、资源与参数检查；权限占位当前放行 |

`allowed-namespaces: []` 沿用原语义：允许所有 Namespace。生产权限阶段将另行设计用户的显式作用域，不能把这个空列表当作默认拒绝。

运行主机需要安装 kubectl、Helm（当前命令适配面向 Helm 3），在服务 PATH 中可用或通过 `kubernetes.kubectl` / `kubernetes.helm` 指定。现有 Dockerfile 的运行镜像没有安装这些 CLI，需要提供包含 CLI 的运行镜像或挂载受信任的兼容二进制；只有 client-go 查询不依赖 CLI。

默认从 `kubeconfig.Provider` 读取当前用户 `~/.kube/config`。文件 Provider 解析相对 CA/证书/私钥/token 文件引用，临时复制后仍指向原文件。命令临时 kubeconfig 和 values 使用 0600，操作结束后删除；不输出凭据。

`kubernetes.in-cluster: true` 时入口为 client-go、kubectl、Helm 统一选择 InClusterProvider，使用相同 ServiceAccount 集群连接，不再让 CLI 回退至另一份本地 kubeconfig。当前仍为单集群，工具不接受连接地址或 kubeconfig 参数。数据库 Provider 仍是适配接口，数据库加密与权限未实现。

默认监听回环地址；改为非回环仍须设置 `CONTROL_API_KEY` 保护现有 REST 入口，但这个 Key 不作用于公开 MCP 路由。

客户端选择 Streamable HTTP 并填入 MCP URL。也可使用 MCP Inspector：

```bash
npx @modelcontextprotocol/inspector
```

## 只读工具（15 个）

所有读取仍受固定集群和 Namespace 配置约束。StorageClass、GatewayClass 为明确开放的集群级摘要。受限 Namespace 配置下，集群状态只返回可见 Namespace 数量。

| 工具 | 参数 | 结果与边界 |
| --- | --- | --- |
| `cluster_status` | `{}` | 集群版本与可见 Namespace 数量 |
| `list_namespaces` | `{}` | 可见 Namespace 列表 |
| `list_storage_classes` | `{}` | 存储类摘要 |
| `list_resources` | `namespace` | 原有 Deployment、StatefulSet、Pod、Service、PVC 摘要 |
| `list_gateway_resources` | `namespace` | GatewayClass、Gateway、HTTPRoute、TCPRoute 摘要；缺失资源记录到 missingResources |
| `get_resource` | `namespace, kind, name` | 白名单资源的 spec/status 与基本身份信息；过滤注解、ConfigMap 数据、env.value、command/args 等 |
| `list_resource_objects` | `namespace, kind, labelSelector?, limit?, continue?` | 白名单内任意一种资源的分页详情，过滤规则同 get_resource；可查 Job、CronJob 等 |
| `list_events` | `namespace, kind?, name?, limit?, continue?` | Namespace 事件，支持指定对象过滤 |
| `get_pod_logs` | `namespace, pod, container?, tailLines?, sinceSeconds?, previous?, timestamps?` | 非流式日志，支持上一实例和时间范围 |
| `get_rollout_status` | `namespace, kind, name` | Deployment/StatefulSet/DaemonSet 就绪快照，不阻塞等待 |
| `get_workload_history` | `namespace, kind, name, limit?, continue?` | 从所属 ReplicaSet/ControllerRevision 读取历史版本号，不返回历史 Pod 模板 |
| `list_helm_releases` | `namespace, limit?, offset?` | 分页 release 摘要，不跨 Namespace 查询 |
| `get_helm_status` | `namespace, release` | release 版本、状态、chart 摘要 |
| `get_helm_history` | `namespace, release, limit?` | Helm revision 摘要 |
| `validate_manifest` | `namespace, manifest` | 本地 YAML/GVK/Namespace 校验；不访问集群、不要求写开关 |

分页默认 100，最大 200。Kubernetes 列表用返回的 `continue` 读取下一页；Helm 列表用 offset。工作负载历史先分页查询再按控制器 UID 过滤，因此某页可能为空但仍有下一页，不能仅凭空 items 判断结束。历史可能已被 revisionHistoryLimit 清理。

日志默认最近 200 行，最大 5000 行；sinceSeconds 最大 7 天；读取上限 256 KiB，返回 truncated。日志、事件 message、资源 status 可能包含业务敏感内容或不可信文本，通用字段过滤不能保证其中没有秘密。后续权限可独立限制 `pod.logs` / `event.list` / `resource.get`。

Helm 查询只序列化固定摘要字段，不返回 values/config、manifest、NOTES、hooks、description。只读查询仍可能需要集群侧读取 Helm 存储 Secret 的 RBAC，服务端不会把这些 Secret 内容暴露为工具结果。

部署状态不保证应用能接收业务流量。缩容到 0 可以 complete=true；StatefulSet 分区更新和 OnDelete 策略采用“全部副本更新且 revision 一致”的保守完成条件。

Gateway 专用列表在 NotFound/MethodNotSupported 时回退版本，Forbidden 等直接失败。通用详情/删除通过 discovery 在该 kind 的固定版本列表内选取已服务且 namespaced 的资源，不接受任意 GVR。

## 写工具（12 个，均已实现）

每个工具都经过业务层写总开关、Namespace/资源检查和预留 Authorizer。只有开启 MCP 写开关才注册。

| 工具 | 参数 | 行为 | 预留 action |
| --- | --- | --- | --- |
| `dry_run_manifest` | `namespace, manifest, serverSide?` | kubectl apply --dry-run=server，无持久化变更 | `manifest.preview` |
| `apply_manifest` | `namespace, manifest, serverSide?` | kubectl apply，支持多文档 YAML | `manifest.apply` |
| `helm_upgrade` | `namespace, release, chart, version?, valuesYaml?, wait?` | upgrade --install，下载并预检查 chart | `helm.upgrade` |
| `scale_workload` | `namespace, kind, name, replicas` | Deployment/StatefulSet，0～10000，保留 scale resourceVersion | `workload.scale` |
| `restart_workload` | `namespace, kind, name` | Deployment/StatefulSet/DaemonSet，通过 Pod template 时间戳触发重启 | `workload.restart` |
| `rollback_workload` | `namespace, kind, name, revision` | 三类工作负载回滚，必须指定正 revision | `workload.rollback` |
| `delete_resource` | `namespace, kind, name` | 白名单中单个资源；绑定 UID，后台级联，不接受 --all 或任意 selector | `resource.delete` |
| `helm_rollback` | `namespace, release, revision, wait?` | 指定正 revision 回滚，检查当前与目标 manifest | `helm.rollback` |
| `helm_uninstall` | `namespace, release, wait?` | 卸载及删除 release 关联资源 | `helm.uninstall` |
| `create_namespace` | `namespace` | 创建配置允许范围内的 Namespace | `namespace.create` |
| `delete_namespace` | `namespace` | 删除整个 Namespace 及其内容；禁止 default 和 kube-* | `namespace.delete` |
| `update_configmap` | `namespace, name, data` | merge patch 已有 ConfigMap 的指定 data 键，保留其他键，不自动重启 | `configmap.update` |

删除返回 accepted 不表示 finalizer 已完成或 PVC 数据仍保留。Namespace 删除是显式的大范围操作，会删除其中的所有类型资源，不能把通用单资源白名单解释为它的子资源授权。回滚、扩缩容、重启返回请求结果，随后通过状态/事件/日志查询确认。滚动重启受 StatefulSet/DaemonSet 的 OnDelete、partition 等策略影响，未必立即替换所有 Pod；HPA 也可能覆盖手动扩缩容。

多文档 apply 和 Helm 变更不保证事务性，失败可能留下部分变更。工具不会自动重试写入，超时后先查询状态再决定后续动作。回滚只恢复 Kubernetes/Helm 配置，不恢复数据库和存储内容。

ConfigMap 更新的 data 必须非空，键符合 Kubernetes 规则，编码后不超过 1 MiB。不提供读取完整 ConfigMap data 的工具，写结果也不回显数据；不要使用 ConfigMap 保存凭据。

## 资源白名单

YAML apply、通用详情/列表/删除共享 `internal/domain/resources.go` 的固定类型表；manifest 还必须匹配具体 API group/version，防止同名 CRD 绕过 kind 检查。

| API group/version | kind |
| --- | --- |
| v1 | ConfigMap、Pod、Service、PersistentVolumeClaim |
| apps/v1 | Deployment、StatefulSet、DaemonSet |
| batch/v1 | Job、CronJob |
| networking.k8s.io/v1 | Ingress、NetworkPolicy |
| autoscaling/v2 | HorizontalPodAutoscaler |
| policy/v1 | PodDisruptionBudget |
| gateway.networking.k8s.io/v1 或 v1beta1 | Gateway、HTTPRoute |
| gateway.networking.k8s.io/v1alpha2 或 v1 | TCPRoute，具体取决于集群实际服务版本 |

Namespace 只通过专用工具管理。Secret、RBAC、CRD、任意集群级资源不在通用白名单；不提供任意 shell/kubectl/Helm 参数、Pod exec、kubeconfig 导出或节点管理。名称不接受路径、标志位等注入内容。

## Helm 执行细节与限制

- 接受已配置仓库的 `repo/chart` 或 `oci://registry/path`；不接受任意本地路径、HTTP URL 或额外 CLI flags。仓库/registry 凭据由运行环境配置，不通过工具参数传入。建议明确指定 version。
- 先拉取 chart 到私有目录，再用同一 tgz 渲染与安装，避免两次下载版本变化。values 为私有临时文件。
- 检查现有 release manifest（upgrade/rollback/uninstall 都可能删除旧对象）；升级还检查新渲染资源；回滚检查指定 revision。
- 当前使用 --no-hooks，不执行 chart hooks；不自动安装 CRD 或创建 Namespace。模板包括 CRD 检查，超出资源/Namespace 白名单就拒绝。包含 Secret、ServiceAccount、RBAC 等对象的常见 chart 可能因此无法安装，不能声称兼容所有 chart。
- upgrade 使用 --reset-values，新部署取 chart 默认值和本次 values，不隐式继承旧 values；调用者须提交想保留的覆盖配置。
- 命令输出只返回固定完成说明，避免 Helm NOTES 泄露信息；执行失败返回工具错误，不暴露 stderr。
- **预渲染检查是功能性前置检查，不是完整安全沙箱**。Helm template 与实际 upgrade 的 Capabilities/lookup、并发 release 更新等可能使实际渲染不同；回滚/卸载检查也不是原子授权。后续权限阶段需受信 chart 来源、版本/摘要绑定、实际提交资源校验（如受控 post-renderer）、并发锁和集群侧最小 RBAC；当前未实现这些权限策略。

## 调用示例

初始化：

```bash
curl -sS http://127.0.0.1:8091/api/v1/pub/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"manual-test","version":"1.0"}}}'
```

在初始化后，通过相同 URL 发送 tools/list 或 tools/call。stateless 模式不用 session ID：

```bash
curl -sS http://127.0.0.1:8091/api/v1/pub/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

启用写工具后，以下是 tools/call 的 JSON 请求体例子：

```json
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"scale_workload","arguments":{"namespace":"default","kind":"Deployment","name":"nginx-demo","replicas":2}}}
```

```json
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_pod_logs","arguments":{"namespace":"default","pod":"nginx-demo-实际Pod名称","tailLines":100,"sinceSeconds":600}}}
```

```json
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_workload_history","arguments":{"namespace":"default","kind":"Deployment","name":"nginx-demo"}}}
```

```json
{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"helm_upgrade","arguments":{"namespace":"default","release":"nginx-demo","chart":"demo/nginx","version":"1.0.0","valuesYaml":"replicaCount: 1\n","wait":true}}}
```

上述 chart 为占位示例，需替换成运行环境已配置的仓库/OCI chart，并确保其模板满足白名单。Namespace 必须存在（或先 create_namespace）且符合配置范围。

建议 nginx 验收链路：validate_manifest → dry_run_manifest → apply_manifest → get_rollout_status → list_resource_objects(kind=Pod) → get_pod_logs/list_events → scale_workload/restart_workload → get_workload_history/rollback_workload → delete_resource。删除 Deployment 和 Service 不等于卸载 Helm release；用对应的管理方式清理资源。

## 测试与验证

测试使用真实 SDK 客户端，经 Gin → MCP → application → 模拟 Kubernetes HTTP。外部命令使用测试程序自身作为独立子进程，验证 argv、stdin、临时文件权限与清理，不执行真实 kubectl/Helm 或连接用户集群。

覆盖：默认 15 个读工具、启用后 27 个工具、读写标注、各功能调用、缩容到 0、scale 乐观锁、删除 UID precondition、扩容/重启/回滚参数、日志参数及 256 KiB 上限、分页/事件、历史资源归属过滤、Helm 阶段与输出过滤、跨 Namespace/Secret/GVK/参数注入拒绝、全局写总开关、权限占位可替换且拒绝发生在副作用前、旧 REST 方法共用占位、CLI 失败/取消、配置环境变量、MCP 关闭、请求 ID 与跨站保护、相对 kubeconfig 引用。

```bash
gofmt -w cmd internal tests
go test ./...
go vet ./...
```

以上自动化验收不等于真实 Helm chart 或集群部署验收。实际集群行为、RBAC、Admission、镜像拉取、存储和网关能力须在目标集群另行验证。

## 参考

- [官方 Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- [SDK 工具说明](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/server.md)
- [MCP 工具标注的限制](https://blog.modelcontextprotocol.io/posts/2026-03-16-tool-annotations/)
- [Kubernetes rollout](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_rollout/)
- [Kubernetes logs](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_logs/)
