---
title: 系统资源与特殊 Pod 保护方案
subtitle: publish 管服务权限，k8s-ctl 管执行边界
lang: zh
template: doc
cols: 2
---

方案状态：待评审，尚未实现。系统资源允许只读排查，拒绝写入。本方案不恢复业务 Namespace 白名单，不限制普通开发容器内的命令内容。

## A 责任与调用链

publish 判断用户能否操作服务和环境。k8s-ctl 判断本次操作是否触及基础设施或危险工作负载。两者都通过后才能执行。业务管理员不能自动跳过系统保护。

```flow LR
用户 -> publish: 服务与环境权限
publish -> HTTP或MCP: 可信身份与目标
HTTP或MCP -> 应用层: 统一参数
应用层 -> 系统保护: 解析实际目标和内容
系统保护 -> 执行适配器: 允许
执行适配器 -> Kubernetes: RBAC与准入
```

现有 `DeferredAuthorizer` 继续作为业务授权扩展点。新增独立系统保护模块，不让默认放行覆盖系统拒绝。

现有 `pod-exec.authorization-enabled` 只控制旧的用户 grants。特殊 Pod 检查不再随它关闭。Shell 命令拦截器保持现有占位职责。

## B 保护哪些对象

| 类别 | 识别方式 | 开发者操作策略 |
|---|---|---|
| Kubernetes 系统 Namespace | 内置 kube-system、kube-public、kube-node-lease | 允许只读；拒绝写入及容器进入 |
| 网关、控制器等基础设施 Namespace | 服务端可信集群策略补充，如 ingress-nginx、higress-system | 允许只读；其他同上，名称按集群实际配置 |
| 混合 Namespace 中的系统组件 | 精确 API Group、资源类型、Namespace、名称及实际对象身份 | 允许只读；拒绝修改、删除、重启、扩缩容及容器进入 |
| 系统组件的子资源 | 查询实际 ownerReferences，逐级核对 UID | Pod 换名和 ReplicaSet 更新不能绕过 |
| 集群级敏感类型 | 保持现有类型限制，显式覆盖 RBAC、Node、CRD、Webhook 等 | 不开放通用写入 |
| 普通业务资源 | 未命中保护，且满足现有能力限制 | 继续按 publish 授权执行 |

系统清单是少量基础设施信息，不是几百个服务的授权列表。不默认保护 `default`，避免误伤已有业务；需要时由运维加入。

策略从控制器配置或受信集群注册通道加载。普通工具参数不得传入跳过标志、豁免身份或修改策略。内置规则不可通过追加配置删掉。

labels 可辅助识别，不能成为唯一依据。保护判断同时看当前对象和待写入内容，禁止先删除标记再操作。Namespace 删除要检查受保护对象，防止级联删除绕过。

系统保护按操作能力判定，不因为命中系统 Namespace 就拒绝全部请求。只读查询不需要系统写入豁免。HTTP 方法、MCP 工具描述和现有 write 标志不能替代操作分类。

| 操作能力 | 系统资源策略 |
|---|---|
| 列表、详情、原始 YAML、状态、事件、Pod 日志 | 允许只读，仍需通过原有认证及 publish 授权 |
| Ingress 规则、ingress-nginx Deployment/ConfigMap、控制器日志 | 允许通过已有或后续补充的只读接口排查 |
| apply、删除、扩缩容、重启、回滚、配置及元数据更新 | 拒绝 |
| Shell、exec、交互终端 | 拒绝任意命令入口，不按 cat/ls 等命令文本推断只读 |
| 文件上传 | 拒绝 |
| 当前文件下载 | 通过 exec 读取；本轮沿用特殊目标执行限制，不自动放开 |

Ingress 对象和 ingress-nginx 控制器是不同资源，两者都支持上述只读原则。支持读取不等于新增任意目标的 publish 工具；仍使用受信目标解析与授权。

原始内容保持现有输出策略，本轮不增加脱敏或扩大资源类型支持。未来若需系统容器文件读取，使用单独受约束的读取能力，不开放任意 exec。

## C 特殊 Pod 与危险模板

| 检查项 | 处理 |
|---|---|
| hostNetwork、hostPID、hostIPC | 拒绝进入、上传下载及创建该配置 |
| privileged、hostPath | 同上，检查全部容器与卷 |
| 危险 capabilities、Unconfined、非默认 procMount 等 | 按明确的 Pod 安全规则检查 |
| 系统组件及控制器自身 Pod | 即使没有特权字段，也拒绝进入 |
| 已终止、删除中或 UID 已变化 | 保留已有冲突与身份检查 |
| 普通业务 Pod | 保持任意 Shell、TTY 和文件传输 |

检查普通容器、initContainer 和 ephemeralContainer。检查 Pod、Deployment、StatefulSet、DaemonSet、Job、CronJob 中对应的 PodSpec。

PVC 本身不等于 hostPath，不直接拒绝普通持久化。容器 root 用户也不直接等于特权 Pod，不要求本轮统一改成 non-root。

本轮不强制把 allowPrivilegeEscalation 改为 false，也不宣称完整实现 Restricted 标准。采用有版本的明确规则集，先覆盖宿主机访问与明显特权。

已有危险 Pod 仍允许按原授权读状态和日志；容器命令、终端和传输拒绝。业务变更不能生成危险模板。特殊工作负载的修复由运维通道处理。

## D 覆盖所有执行路径

| 入口 | 接入检查 |
|---|---|
| 资源列表、详情、YAML、事件、日志 | 按只读分类放行系统保护，不被 Namespace 写保护误拦截 |
| delete、scale、restart、metadata、ConfigMap 等 | 实际对象、所属 Namespace、系统身份与操作类型 |
| Manifest validate | 保持本地校验；增加可离线判断的系统规则 |
| Manifest apply 与 dry-run | 写入前在线解析目标，检查全部文档及 PodSpec |
| Helm upgrade、rollback、uninstall | 复用已有新旧 Manifest 校验；保持禁用 hooks |
| 工作负载 rollback | 检查将恢复的版本模板，防止恢复危险配置 |
| Shell、终端、文件 | 共用目标检查，不只保护 Shell 命令入口 |
| 终端写入、文件票据使用 | 重新检查目标、UID 及当前策略 |

Manifest 批次先全量检查，再执行任何写操作。一项被拒绝则整个请求不执行。List 等包装类型如继续不支持，则明确拒绝，不跳过内部对象。

本地 validate 通过只说明离线检查通过，不代表在线保护或 RBAC 已通过。实际写入和 server dry-run 必须重新检查。

Helm 同时检查历史资源，防止升级移除、回滚或卸载间接删除保护对象。保持对同一 Chart 包的校验与执行。

应用层保护仍存在检查与执行之间的竞争窗口。不能宣称执行前读取一次对象即可消除竞态；能用 UID/resourceVersion 的地方使用前置条件。

## E 独立模块与错误契约

建议新建以下模块，名称为设计草案：

```tree
internal
  domain
    protection
      policy.go | 保护目标、规则与决定
  application
    protection
      guard.go | HTTP与MCP共用执行保护
      manifest.go | 资源和Pod模板检查
      pod.go | 容器进入检查
  infrastructure
    kubernetes
      protection_resolver.go | 查询对象与所有者链
```

复用现有错误、目标类型与 Kubernetes adapter。纯规则只接收对象快照；查询适配器负责加载真实对象。不要让每条规则直接访问 Kubernetes。

需要现有对象的判定无法完成时停止执行。对象不存在返回 404，上游不可用返回 503，确定命中保护返回 403。不要把查询失败误判为普通业务 Pod。

建议稳定原因：`system_namespace_protected`、`system_resource_protected`、`unsafe_pod_target`、`unsafe_workload_spec`、`reserved_metadata_key`。需扩展现有错误映射，保证 HTTP/MCP 保留同一原因。

拒绝响应只返回规则、目标、危险字段和 requestId。不返回命令原文、Secret 或 kubeconfig。

策略启用后，已有终端需要复检并终止违规会话。已启动的远端进程或数据流不保证能被瞬时撤回，需明确取消与生效语义。

## F 防止侧路与合法例外

Namespace labels/annotations 可影响安全机制。禁止普通元数据操作删除或降低 Pod Security Admission 标签、修改保护标记和其他已登记的准入控制键。键清单要集中维护。

普通业务 Ingress、Service 不因类型相同而一律禁止。共享 LB、网关对象、系统 Release 等精确保护。公网域名、NodePort、externalIPs 等跨服务影响另列平台与集群策略，不宣称本轮全部覆盖。

Catalog/Higress 管理不能得到通用管理员豁免。若启用该能力，只允许内部受信路径对指定网关对象、指定字段做受限变更；请求参数不能自称该能力。当前 operator 不开放 Catalog，可以先维持关闭。

保留普通容器任意 Shell 后，不能靠扫描命令文本证明安全。应检查业务 ServiceAccount 的实际权限，限制能访问的高权限凭据及网络；这些是独立边界。

Kubernetes RBAC 是允许规则的累加，没有用于覆盖其他授权的 deny 规则。广泛的 cluster-admin 不能靠新增一个限制 Role 消除。需要使用适当的执行凭据，并由集群准入拦截危险工作负载。

集群 Pod Security Admission 可先 audit/warn，再由运维评估 enforce。现有规则与准入要配套，避免平台允许删除限制标签。

参考：[RBAC 官方实践](https://kubernetes.io/docs/concepts/security/rbac-good-practices/)、[Pod 安全标准](https://kubernetes.io/docs/concepts/security/pod-security-standards/)、[Pod 安全准入](https://kubernetes.io/docs/concepts/security/pod-security-admission/)。

## G 实施与验收

1. 独立系统保护模块，拆开 grants 和特殊 Pod 检查。
2. 接入资源写入、Manifest、Helm 与回滚。
3. 接入终端和文件的准备及实际使用。
4. 补稳定错误、配置说明和覆盖矩阵。
5. 先跑模拟测试，再用隔离资源验收。

| 场景 | 预期 |
|---|---|
| 查询系统 Namespace 的 Ingress、配置、事件和日志 | 通过原有授权后可读 |
| 对同一系统资源读取与修改 | 读取成功；修改在执行前拒绝 |
| POST 形式的日志或 YAML 查询 | 按实际只读能力处理 |
| 普通开发 Pod 的 Shell、TTY、上传下载 | 继续可用 |
| authorization-enabled=false 操作特殊 Pod | 仍拒绝 |
| HTTP 与 MCP 操作同一系统资源 | 同一保护结果 |
| 批量 YAML 混入危险对象 | 执行调用数为零 |
| 删除保护标记、降低安全标签 | 拒绝 |
| 系统组件子 Pod 或普通 Namespace 中的特权 Pod | 不能进入 |
| Helm 旧版本或回滚版本含保护对象 | 执行前拒绝 |
| Pod 替换、所有者查询失败、策略变更 | 不沿用过期判定 |
| 关闭 Shell 命令拦截器 | 系统保护仍生效 |
| 受信 JWT 或业务管理员 | 不能覆盖系统拒绝 |

运行 gofmt、go test ./...、go vet ./...。会话取消和策略复检增加 race 验证。真实测试不创建特权 Pod、不修改系统组件；通过 mock 和隔离保护目标验证拒绝路径。

本轮不实现用户权限中心、危险命令语义分析、沙箱及完整网络隔离，也不自动改真实集群 RBAC 或准入策略。

## H 对象如何登记与匹配

Kubernetes 不提供能完整标识全部基础设施的统一标签。内置 Namespace 规则覆盖核心系统；集群特有基础设施必须由运维登记。未知组件不能仅凭名称被可靠识别。

首期将登记放在 k8s-ctl 的服务端配置中，按 clusterId 隔离。publish 继续管理服务权限，不在每个业务请求中传入保护清单。后续可以迁移至 publish 的集群管理配置，并仅通过受信注册接口同步。

当前 operator 注册输入只有 clusterId、kubeconfig 和已弃用字段。它还不支持保护策略；以下为拟新增配置，不是现有可用字段：

```yaml title="拟新增配置 · 示例名称须按实际集群核对"
protection:
  clusters:
    lucy-mid-k8s:
      additional-namespaces:
        - ingress-nginx
        - higress-system
      resources:
        - group: apps
          resource: deployments
          namespace: platform
          name: k8s-ctl-operator
          protect-dependents: true
        - group: ""
          resource: services
          namespace: platform
          name: k8s-ctl-operator
        - group: ""
          resource: configmaps
          namespace: platform
          name: k8s-ctl-operator-config
```

内置 kube-system、kube-public、kube-node-lease 不必重复配置。`platform` 和对象名称仅为示例，不是本次已查到的部署。控制器可以位于独立集群，不能假设每个受管集群都有一个自身 Pod。

| 识别来源 | 如何使用 | 能否单独认定受保护 |
|---|---|---|
| 内置与运维登记的系统 Namespace | 精确匹配 Namespace 名称 | 能，覆盖其中全部对象及 Namespace 自身 |
| 运维登记的具体资源 | 匹配 clusterId、API Group、resource、Namespace、name | 能；删除重建后同名对象继续受保护 |
| 对象的实际 ownerReferences | GET 所有者并核对 UID，追溯到保护根对象 | 能识别真实从属对象，不能替代根对象登记 |
| Pod 与模板的安全字段 | 检查实际或拟创建的 PodSpec | 能认定危险 Pod，无需登记 Pod 名称 |
| app 名称、镜像名、推荐 labels、Helm labels | 生成待运维核对的候选项 | 不能作为唯一可信依据 |

核心资源 Group 为空字符串。资源标识使用规范化的 API Group/resource，防止 Kind 别名或版本差异绕过。GVK 到资源的映射复用现有固定表，超出支持范围则明确拒绝。

普通写入与容器执行按以下顺序检查。只读请求先按原授权放行，不执行这条写入拒绝链：

1. 从受信路由取得 clusterId，规范化目标。
2. 命中系统 Namespace 则拒绝写入或进入。
3. 命中精确保护资源则拒绝。
4. 读取现有对象并追溯真实所有者链。
5. 检查 PodSpec 是否包含危险配置。
6. 全部通过，再调用现有执行逻辑。

Namespace 自身用 metadata.name 匹配，不使用空的 metadata.namespace。创建请求检查拟创建对象，不能因 GET 返回 404 就认定安全。更新同时检查旧对象与新对象，防止删除 ownerReference 或保护标记后绕过。

所有者链设置深度上限，检测循环并核对 UID。对已有所有者引用无法完成查询时，写入或进入失败关闭；确认没有引用的独立 Pod 继续检查其他规则。

例如：操作 ingress-nginx/controller 的 ConfigMap，Namespace 规则直接命中。读取允许，更新拒绝。操作 platform 中控制器的 Pod，则沿 Pod、ReplicaSet、Deployment 的实际 UID 链追溯保护根。

Service、ConfigMap、Secret、PVC 等不保证有到 Deployment 的 ownerReference，不能靠 Pod 追溯自动覆盖。独立系统 Namespace 可整体保护；混合 Namespace 必须补齐精确对象清单。对清单外、无危险字段的未知系统组件，首期无法承诺自动识别。

上线前，运维核对 Namespace 与系统组件清单。工具可辅助生成候选清单，不能将所有 Helm Release 或所有带 app 标签的资源自动认定为系统资源。
