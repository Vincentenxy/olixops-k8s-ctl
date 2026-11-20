# Namespace 限制调整与现有校验

日期：2026-11-13。范围：当前 k8s-ctl 和 IDEA 中的 publish-devops-api。

## 当前结论

已移除 publish 与 k8s-ctl 基于静态 Namespace 名单的权限限制。用户通过 publish 提供服务 ID、环境等业务参数；publish 复用服务权限及工作负载归属检查，再从服务数据解析 clusterId、Namespace、release 和 Pod/容器目标。不再按服务数量维护配置名单。

下层 HTTP/MCP 仍需要明确目标 Namespace；平台提供的八个 k8sctl MCP 工具不增加 Namespace 覆盖参数。可信平台注册仅需要 clusterId 和 kubeconfig。后续 Namespace/资源授权在权限模块实现，不放回配置名单。

## 已移除的限制

| 位置 | 本轮行为 |
| --- | --- |
| publish `CtlOperatorBootstrap.ensure()` | 不比较 Namespace 名单，不向下层下发名单；Namespace 不参与初始化缓存身份 |
| `internal/interfaces/operator/registry.go` | 不要求提供 allowedNamespaces，不限制 Namespace 个数；旧字段兼容接收后丢弃，不参与配置摘要或实例装配 |
| `cmd/main.go` | 不向应用或 exec 执行器装配 Namespace 名单 |
| `internal/application/cluster_service.go` | 删除通用名单判断；不再按名单过滤 Namespace 列表与统计；requireNamespace 仅检查名称格式 |
| `internal/application/manifest.go` | 删除名单判断，保留请求目标与 YAML Namespace 必须一致的检查 |
| `internal/application/operations.go` | Helm 渲染校验复用更新后的 Manifest 校验，不再做 Namespace 名单授权 |
| `internal/application/catalog_cluster.go` | 经公共应用方法自然取消名单限制，仍执行授权和预检 |
| `internal/application/pod_exec.go` | 删除 exec 专用名单，以及按 default、olixops、kube-* 名称拒绝 exec 的逻辑 |
| `internal/application/terminal.go` | 删除终端与文件共用的 exec Namespace 名单检查 |
| `internal/infrastructure/config/config.go` | 取消名单非空、子集与保留 Namespace 的启动校验 |

## 配置兼容与升级

`kubernetes.allowed-namespaces`、`pod-exec.allowed-namespaces` 和 publish `backend.allowed-namespaces` 仅保留废弃属性绑定，不再生效。注册接口的旧 allowedNamespaces 字段同样忽略。当前配置和部署模板已删除这些字段，无需为数百个服务逐项配置。

旧名单变化不应替换运行实例，否则会破坏终端/文件引用。下层在计算配置摘要前清空旧字段；publish 缓存与 Namespace 无关，但保留集群、环境和凭据绑定。正常切换服务 Namespace 不重建运行实例，实际 kubeconfig 变化仍重建。旧下层二进制仍要求名单，升级时先部署新下层，再更新 publish；两层都使用新代码后此规则生效。

原独立入口也不再按静态名单授权，其目标范围由现有 Authorizer 扩展及 Kubernetes RBAC 约束。当前默认通用 Authorizer 仍放行，不能把 JWT 身份验证当作服务权限隔离。平台操作器应由 publish 通过受信 JWT 访问，不直接对开发者开放内部控制与操作入口。

## 继续保留的检查

- publish 既有身份、服务可见范围、环境、LOGS_READ / TERMINAL、服务工作负载归属与运行内容访问权限
- Namespace 名称合法性、非空目标、YAML 目标一致性、资源种类、参数类型与大小等契约校验
- 认证、JWT audience、HTTP/MCP/写开关、Shell 显式开关、Authorizer 扩展及 Kubernetes RBAC
- 旧受限诊断模式的精确用户/Pod/容器/action grants；其中 Namespace 是具体授权目标，不是全局静态名单，本轮没有删除此兼容权限模式
- Pod 标签 k8s-ctl/exec-enabled=true 或精确 UID grant、运行状态与既有 hostPath/privileged/hostNetwork 等执行资格检查
- Shell 独立拦截扩展点，当前 AllowAll 仅占位放行
- 用户会话归属、Pod UID、runtimeId、有效期、单次文件引用和文件完整性
- default、kube-* 的 Namespace 删除保护；这属于专用删除操作保护，不限制在其内查询、执行 Shell 或传输文件
- 容器自身文件系统权限，包括只读挂载与运行用户

取消 Namespace 名单不等于所有 Pod 自动具备 Shell 执行资格；本轮不改 Pod 标签、不补 grants、不修改业务容器或文件。

## 验证

回归测试包括：无名单注册；旧名单超过 100 项仍不限制注册；旧名单变化不替换 runtime；旧配置名单与目标不匹配时资源/日志、exec、终端和文件通路继续工作；default/kube-* 等名称不再单独阻止 exec；授权策略拒绝、认证失败、Namespace 格式错误与 YAML 目标不一致仍被拒绝。

publish 接入相关 50 项 Java 测试通过，Go 全量测试、vet、指定 race 和文档生成均通过。本地操作器已更新重启；通过真实 publish MCP 查询服务 2963078505 / sim 日志成功，解析 Namespace 为 setting-efficient，无需配置名单。详情见 [本轮测试记录](testing/namespace-service-authorization-2026-11-13.md)。本轮没有执行真实容器文件写入，日志查询与模拟端点通过不代表目标业务 Pod 已具备执行资格。
