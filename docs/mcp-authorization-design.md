# MCP / REST 权限控制设计（尚未实现）

更新时间：2026-10-18。本轮只实现操作功能与可替换校验接口；下文的身份、角色、策略、审批、OAuth 与审计存储均为后续设计。

## 现有预留点

`internal/domain/authorization.go` 定义：

```go
type Authorizer interface {
    Authorize(context.Context, AuthorizationRequest) error
}
```

请求包含 Cluster、Action、Write、Targets、PayloadDigest。Target 带 API version、kind、Namespace、name；资源类型省略 API version 的复合查询，后续策略须按实际涉及的资源展开处理。PayloadDigest 是变更参数的 SHA-256 摘要，用于未来绑定审批，不是签名或权限凭据。

`application.WithAuthorizer(...)` 可替换适配器。`ClusterService` 在底层 Kubernetes/CLI 调用前执行校验，包括已有 REST ApplyManifest/HelmUpgrade 方法，避免只在 MCP handler 中检查。Helm 还会对渲染或历史 manifest 的资源逐项调用接口。

**默认 DeferredAuthorizer 返回 nil，无任何认证与授权。** 测试使用拒绝适配器，只证明预留点有效，不表示生产权限已完成。MCP 写开关只影响工具注册，Kubernetes 写总开关影响业务执行；都不识别调用者。当前 Cluster 为单集群别名 `configured`，后续接入集群注册后替换为稳定的业务 cluster ID。

## 建议的请求链路

```text
请求（REST / MCP）
  ↓
传输层凭据验证 → 可信 Principal 写入 context
  ↓
业务层构造 action + 集群 + 目标资源 + 参数摘要
  ↓
Authorizer：身份、角色、作用域、内容策略、必要审批
  ↓
记录允许/拒绝决策
  ↓
受限 Kubernetes 身份执行
  ↓
记录完成/失败/部分完成与请求 ID
```

模型传入的 userId、role、isAdmin、confirmed 等都不能作为身份或授权依据。身份必须由服务端认证过程建立，context 的 principal 类型需由认证模块控制。

## 认证：先识别谁在调用

小规模受控环境可以先用“每个调用方一枚独立 token”，token 通过请求头传入，仅存哈希并支持到期、撤销和轮换。不能继续将所有调用者共用的一枚 Control API Key 当作用户级权限系统。

如果需要标准 MCP 客户端的交互式登录，则接入现有身份平台的 OAuth，提供协议要求的 Protected Resource Metadata 和认证挑战。服务端验证 token 的签名/有效性、issuer、audience、期限与 scopes，不能把任意第三方 token 透传到 Kubernetes。该选择取决于客户端、身份平台与部署方式，当前未选择或实现。[MCP 授权规范](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/index.mdx)

普通读工具可继续匿名。传输认证层应支持“匿名读、写需身份”，不能一加全局中间件就强迫所有只读查询登录。如果同端点按方法区分认证不利于所选客户端的 OAuth 发现，可将匿名读 MCP 与受保护操作 MCP 分开，但最终调用仍必须经过相同业务层 Authorizer。

## 授权：识别后判断能做什么

建议先使用“角色 + 操作 + 作用域”，不急于引入独立策略平台。

| 维度 | 决策规则 |
| --- | --- |
| 身份 | 匿名写拒绝；凭据失效或撤销则拒绝 |
| Action | 按 manifest.apply、workload.scale、resource.delete 等独立授权 |
| 集群 | 只允许业务注册的 cluster ID，客户端不能传 kubeconfig/连接地址 |
| Namespace | 明确允许的写入范围；没有匹配规则就拒绝 |
| Group/kind | 使用服务端解析、校验后的资源类型，不能仅凭工具名授权 |
| 资源名称 | 可选的具体名称约束，如仅 nginx-demo；避免随意引入复杂 glob 规则 |
| 内容 | 镜像仓库、ServiceAccount、特权、hostNetwork、hostPath 等规则 |
| 审批 | 按动作与环境判定，如生产删除需单次批准 |

建议角色仅作为策略复用方式：

| 角色示例 | 能力 |
| --- | --- |
| anonymous-reader | 配置范围内的普通摘要查询 |
| observer | 额外允许指定 Namespace 的日志/事件/详情 |
| workload-operator | 指定 Namespace 工作负载的部署、扩缩容、重启 |
| release-operator | 受信 chart 的安装升级与回滚 |
| namespace-admin | Namespace 创建与删除等大范围动作 |

真实策略默认拒绝；空列表不能隐式允许所有写入。普通工具的读开放不意味着所有未来读取都开放，日志和事件需保留独立 action。对未登录但可读的调用者，服务端仍检查固定集群与配置作用域。

## 所有写动作与额外语义

| Action | 需要检查的对象与参数 |
| --- | --- |
| manifest.preview | 所有 YAML 对象，Namespace/GVK/字段范围；不能用预校验权限执行真实写入 |
| manifest.apply | 所有 YAML 对象；已有对象 diff 与创建/更新语义 |
| workload.scale | 工作负载名称、目标副本数，生产可限制区间或禁止缩为 0 |
| workload.restart | 工作负载及其 Pod template；操作可能重复触发 |
| workload.rollback | 当前工作负载、目标 revision、回滚后的实际内容 |
| resource.delete | 单个目标 UID 与删除传播影响；PVC 删除另设规则 |
| configmap.update | ConfigMap 名称、允许键范围、值大小；摘要绑定内容 |
| helm.upgrade | release、来源、version/chart 摘要、values 摘要、实际渲染对象 |
| helm.rollback | 当前与目标 release/revision/资源集合 |
| helm.uninstall | release 及将被删除的所有资源 |
| namespace.create | 新 Namespace 及后续归属信息 |
| namespace.update | 仅 Namespace labels/annotations 的设置与显式删除键，不允许修改其他 metadata/spec/status 字段 |
| namespace.delete | 整个 Namespace 与全部子资源的删除权，不可沿用单资源删除权 |
| resource.yaml.get | 固定 namespaced 资源的原始 YAML 读取；字段过滤策略预留在授权层 |
| namespace.yaml.get | Namespace 的原始 YAML 读取；按 Namespace 作用域限制 |

YAML apply 可以绕过独立工具完成扩缩容、重启和配置更新。需要二选一并明确：把 manifest.apply 定义成范围内完整配置修改权，或者对 diff 做字段级授权，再分别检查 workload.scale/configmap.update 等动作；不能假设只限制 scale 工具就能禁止通过 YAML 缩容。

工作负载创建本身也可能借用高权限 ServiceAccount、读取 Secret 或挂载宿主机目录。仅禁止 Secret kind 不能解决这些越权路径。内容策略和集群侧 Admission/RBAC 要一起设计。

## Helm 的特殊处理

当前实现已有预渲染与存量 manifest 白名单检查、固定下载 artifact、禁用 hooks。这些不构成原子授权：lookup、Capabilities、并发变更可能使模板检查与实际执行不同。

正式权限需补：允许的仓库/registry、固定版本及摘要、实际提交对象的校验（受控 post-renderer 或等价机制）、release 并发锁、revision 绑定、hook/CRD/ServiceAccount/RBAC/Secret 的独立策略。回滚/卸载也要覆盖旧资源，不能只看新 chart 或 release Namespace。

权限接入前不应声称任意 chart 都安全或支持所有 chart。当前白名单会拒绝包含未开放类型的 chart。

## 集群侧兜底

业务权限控制调用者能提出哪些操作；Kubernetes RBAC 控制服务身份实际能执行哪些操作。两者都需要。建议给不同集群/环境配置专用最小权限身份，按 Namespace 绑定 Role，而不是让整个控制服务使用 cluster-admin。

Namespace 管理等集群级动作可以使用单独的管理身份，避免普通部署权限附带 Namespace 删除权。Helm 存储 release 常需要 Secret 权限，必须单独评估与隔离。RBAC 的能力与权限提升路径以 Kubernetes 官方规则为准。[Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)

## 审批与审计

如果要求人工审批，服务端保存：申请人、批准人、集群、资源集合、动作、规范化参数/实际变更摘要、有效期、策略版本与单次消费状态。执行前重新核对目标 UID/resourceVersion/revision 与摘要，防止批准后替换 YAML 或升级目标。当前 PayloadDigest 只是预留材料，尚未实现上述绑定和持久化。

记录请求 ID、可信主体、action、集群、资源、决策原因、审批 ID、执行结果与时间。不记录 token、kubeconfig、证书、完整 YAML/values、日志正文。拒绝也要记录。现有工具成功/失败日志不是完整审计账本。

## 推荐实施次序

1. 确认客户端、部署环境和身份来源，确定独立 token 或 OAuth 接入方案。
2. 建立 Principal 与凭据生命周期；匿名读保留。
3. 实现真实 Authorizer，先覆盖动作、集群、Namespace、kind/name，默认拒绝。
4. 明确 apply 的字段权限语义、Helm 来源/实际资源校验与 Namespace 删除管理权限。
5. 接入集群侧最小权限身份、审计存储与需要的审批。
6. 用 REST/MCP 两条链路验证：未登录不可写、越作用域拒绝、批量部分越权整体拒绝、直接调用/工具别名不能绕过、凭据撤销即时生效、审批内容替换被拒绝。
