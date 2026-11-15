# MCP 系统资源边界全量审计

版本：0.1.0-alpha.2  
日期：2026-11-15

## 结论

**当前不能保证所有开发人员 MCP 入口都禁止修改系统资源。** 服务归属、资源类型白名单和 Kubernetes RBAC 已提供部分限制，但代码尚无独立且始终生效的系统资源写入保护。认证成功、环境放行、服务权限通过，不应自动代表允许操作基础设施。

本次逐项审阅 publish 的 55 个工具和 k8s-ctl 的 57 个工具，检查其共享目标解析、业务执行和间接操作路径。没有逐项执行写工具，也没有修改系统资源。本文为边界审计，不代表这些缺口已经修复。

补充检查发现 API key 模式公开 MCP 和旧 HTTP/WS 的条件性入口问题，另修复权限 DSL、Mapper XML 和中文标签中的重复环境值，见 [扩展检查记录](mcp-system-resource-extended-audit-2026-11-15.md)。

## 范围和入口区别

| 入口 | 数量与核对方式 | 当前状态 |
| --- | --- | --- |
| IDEA publish 项目 | 源码 55；用户 10121993 实际 tools/list 55；名称集合一致 | 37 个只读提示、18 个非只读提示 |
| publish 本地调用的操作器 19091 | 实际 capabilities 查询 | HTTP 开启、MCP 关闭、Catalog 关闭；Kubernetes 写、Shell、终端、文件开启 |
| k8s-ctl 完整源码注册 | 57 个，与逐工具清单核对一致 | 28 个只读、29 个受 MCP 写开关控制；包含可选 Catalog |
| 当前智能体直连 k8s_ctl_diagnostics | 可用工具元数据 57；三个 validate_manifest 请求实际可达 | 与 publish 的 19091 操作器是不同实例；不能混用集群或认证证据 |

只读提示是 MCP 工具描述，不是权限控制。`dry_run_manifest`、终端读取、文件准备等仍可能按非只读工具或能力开关管理。

全部工具及源码位置见：

- [publish 55 工具 CSV](publish-mcp-system-boundary-2026-11-15.csv)
- [k8s-ctl 57 工具 CSV](k8sctl-mcp-system-boundary-2026-11-15.csv)

CSV 的 `registered_in_current_operator` 只指 19091 操作器，不指另一个直连 MCP 实例。每行给出工具、影响、目标来源、现有边界、发现分类和源码证据。

## publish 全量分类

| 类别 | 工具数 | 系统写入结论 |
| --- | ---: | --- |
| 普通查询与元数据 | 31 | 无 Kubernetes 写入；读取内容受各自数据源与认证约束 |
| GitLab/对象存储文本读取 | 2 | 无集群写入；数据读取范围取决于配置的凭据及目标校验 |
| 网络探测 | 2 | 主动探测或 HTTP 访问，不执行 Kubernetes CRUD |
| k8sctl 代理 | 8 | 由服务解析集群、Namespace、Pod；服务归属校验不能替代系统资源分类 |
| 原生集群操作 | 4 | create-namespace、deploy-service、restart-service、scale-service；均需进一步独立基础设施保护 |
| 源配置变更 | 2 | init-service-config、save-service-config 修改后续部署输入，本身不直接部署 |
| 发布单记录 | 4 | 创建记录/快照/事件，不等于执行发布 |
| 发布计划 | 1 | 修改计划图记录 |
| 运维文档 | 1 | 修改文档，不产生 Kubernetes 写入 |

### 服务范围有效，但不是系统资源禁令

`ServiceTargetResolver` 从已认证身份、服务 ID、环境解析真实集群、Namespace、发布名称与选择器，拒绝不支持的部署类型。`ServiceObservationService` 校验 Pod 属于服务；`ServiceContainerService` 对引用保留用户归属、Pod UID 和运行实例检查。`PublishWorkloadAuthorizationService` 也有服务映射、操作授权与实际工作负载校验。

这些校验能拒绝调用者直接传入无关 Pod，但没有独立判断该服务是否属于基础设施。如果系统工作负载被错误登记为普通服务，或者业务 Pod 自身持有高权限凭据/宿主机挂载，现有服务绑定不能证明系统资源不可变。

### publish 原生部署必须单独看

`src/main/java/net/gtjaqh/pubdevops/service/v2/deploy/impl/ServiceDeployServiceImpl.java` 的 `execute`、`resolveChart` 沿 publish 自己的部署/Helm 链执行，不经过 k8s-ctl 的 Manifest 白名单。显式 Chart 名称和版本只校验成对提供，没有看到统一基础设施 Chart 分类。

因此不能用“k8s-ctl 不允许 ClusterRole”推断 `deploy-service` 的所有部署产物也被同一规则保护。外部 Helm 执行器的实际 RBAC、准入策略及最终产物，本次没有通过真实基础设施写入验证；不能宣称已经成功越权。

## k8s-ctl 重点发现

### 1. 通用资源写入缺少系统 Namespace 保护

`internal/application/authorization.go` 调用统一 Authorizer，默认实现 `internal/domain/authorization.go` 的 `DeferredAuthorizer` 返回成功。写开关可以关闭操作，但开启后不等于已经完成系统资源授权。

`internal/application/manifest.go` 检查 YAML、GVK、名称、请求 Namespace 一致性和资源类型白名单，没有禁止系统 Namespace，也没有拒绝特权 PodSpec。

`internal/domain/resources.go` 的通用 Manifest 白名单不含 Node、RBAC、CRD 和 Secret，能阻止这些类型走通用 apply；但白名单中的 ConfigMap、Deployment、DaemonSet、Service、Ingress、Gateway 等仍可能指向系统 Namespace。删除资源、扩缩容、重启、回滚和 ConfigMap 更新同样没有统一系统目标禁令。

`internal/application/operations.go` 的 Namespace 删除只特别保护 `default` 和 `kube-*`，没有覆盖 `ingress-nginx`、`higress-system` 或自定义系统 Namespace。Namespace 创建和 labels/annotations 修改仍是独立的集群范围写能力；不能概括为“所有集群级写入都关闭”。元数据接口只允许 labels/annotations，不代表这些键对系统行为没有影响。

### 2. Helm 类型检查不等于系统发布保护

下层 Helm 升级/回滚复用渲染清单检查和 Namespace 一致性，并禁用 hooks；没有统一禁止系统 Namespace 或系统 release。最终能否修改仍取决于执行凭据与 Kubernetes RBAC。

### 3. Shell、终端和文件有可关闭的 Pod 安全检查

`internal/infrastructure/kubernetes/pod_exec.go:18` 附近的 hostNetwork、hostPID、hostIPC、hostPath、privileged 和控制器自身 Pod 检查，都受 `!execAuthorizationDisabled` 条件控制。关闭 `pod-exec.authorization-enabled` 不只跳过个人授权，还会跳过这些 Pod 执行资格限制。

身份、Pod UID、容器状态、会话/文件引用归属等检查仍保留，但没有不可关闭的系统 Namespace 拒绝规则。文件读写和持续终端复用此目标链；上传保留新建文件、不覆盖已有文件等既有语义，不能当作任意覆盖接口。

开发环境允许任意业务 Shell 命令是现有需求。应把基础设施目标和宿主机/高权限上下文保护从可选业务授权中分离，不能依赖 Shell 命令黑名单解决问题。业务容器持有的 ServiceAccount 权限也是独立边界。

### 4. Catalog 按设计修改共享 Higress

`internal/application/catalog_cluster.go:91` 和 `internal/infrastructure/kubernetes/catalog.go:176` 的端口暴露流程会更新共享 Service 与 Gateway listener，`deploy_service`、`deploy_standard_service` 可触发。它本来就是基础设施变更能力，不能列为“开发用户绝对无法触达系统”的证明。

模板保存、配置保存、准备部署主要写配置/制品；端口同步、预留、释放主要写台账并读取集群，不应误报为每次都会修改 Higress。实例删除有归属/UID 边界，但没有独立系统 Namespace 分类。当前 publish 操作器 Catalog 未开启；完整独立 MCP 可以注册这些工具。

后续需要区分受信平台的入口端口管理和普通开发者的服务操作，既限制系统访问，也保留合法的标准服务端口分配流程。

### 5. 内部 Kubernetes MCP 没有自动向用户开放全部工具

`internal/infrastructure/k8smcp/managed.go` 的托管组件只启用 `pods_exec`，客户端按参数数组调用。它不是当前 57 个外部工具的自动透传来源。远程 sidecar 的网络可达性仍需部署侧约束，不能仅凭托管代码认定所有网络入口都隔离。

## 非破坏性验证

| 场景 | 入口/身份 | 实际结果 | 证据边界 |
| --- | --- | --- | --- |
| 仿真服务 2963078505 传入无关系统 Pod 名称 | publish MCP，10121993 | `[forbidden] Pod 不属于当前服务，或已被替换` | 在执行前拒绝，没有运行命令 |
| kube-system ConfigMap Manifest 校验 | 直连 MCP validate_manifest | valid=true | 仅本地校验，没有 apply，也未验证 Kubernetes 写权限 |
| ingress-nginx ConfigMap Manifest 校验 | 同上 | valid=true | 同上 |
| ClusterRole Manifest 校验 | 同上 | isError=true，forbidden: manifest kind is not allowed | 证明通用 Manifest 类型白名单有效 |
| 相同系统 Namespace 与特权 Pod YAML 检查 | 操作器 HTTP，既有验证证据 | 系统 ConfigMap/特权 Pod 校验通过；ClusterRole 拒绝 | 不等于真实写入成功，见此前边界报告 |
| 业务 Pod 的 ServiceAccount 查询四类系统 patch 权限 | 实际服务 Pod 内 SelfSubjectAccessReview，既有证据 | ingress-nginx Deployment、kube-system ConfigMap、ClusterRole、Node 均 allowed=false | 只证明该 Pod 的该凭据和四个动作，不代表控制器或全部业务 Pod |
| Namespace 授权与 Manifest GVK 定向测试 | 本地 Go tests | 通过 | 测试明确允许策略放行时对 kube-system 做 exec；证明缺少硬限制，而非证明系统保护完整 |

定向测试命令：

```sh
go test ./tests -run 'TestExecNamespaceAuthorizationUsesPolicyInsteadOfConfiguration|TestProtectedNamespacesAndManifestGVK' -count=1
```

实际返回 `ok olixops-k8s-ctl/tests`。本轮未改 Go 源码。更早的非破坏性证据见 [系统资源边界检查](system-resource-boundary-2026-11-15.md)。

附带发现：publish k8sctl 工具对上述拒绝使用 HTTP 200 + 工具错误语义，但内部 envelope 的 code 仍为 -1，未保留实际 403。这不构成放行，也不应据此宣称错误码已全面一致；需要单独修复错误映射。

## 建议后续实现顺序

1. 在共享应用授权入口建立独立基础设施目标策略，HTTP 与 MCP 一致；识别系统 Namespace、系统工作负载、控制器自身、集群级资源及可影响准入的元数据，普通开发身份不得绕过。
2. 把 Pod 的宿主机挂载/特权/系统目标限制与可关闭的个人授权拆开；业务开发 Shell 仍按需求开放。
3. publish 自己的 Helm 部署、Namespace 与工作负载操作接入同一目标分类；检查渲染产物，避免只检查 Chart 名称。
4. 将 Higress 等受信平台基础设施操作单独授权和审计，不通过普通用户参数授权；同步收紧执行凭据的 RBAC 和业务 Pod ServiceAccount。
5. 增加 HTTP/MCP、平台原生部署、终端/文件的系统目标拒绝回归；通过模拟适配器验证拒绝发生在执行前，真实环境只做安全权限查询。

本次只完成常量修改和审计交付，没有实施上述新增基础设施保护，没有变更线上授权或 RBAC。
