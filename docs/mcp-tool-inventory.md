# k8s-ctl MCP 功能清单

核对日期：2026-11-14。依据当前工作区 `internal/interfaces/mcp/` 注册代码、应用层资源定义及 [HTTP/MCP 覆盖矩阵](http-mcp-coverage.md) 整理。本清单说明代码已实现的能力，不等同于某个运行实例已经注册或通过真实集群验收的工具列表。

## 一、总览

完整模式下，开启 Catalog、MCP 写能力、Pod exec 和 Shell，且服务实现对应接口时，最多注册 **57 个 MCP 工具：28 个只读、29 个受写能力开关控制**。

“写能力”是当前注册分类：服务端 dry-run、持续终端读取、下载准备等也归入其中；不能据此认为每项都会持久化修改业务资源。

| 功能类别 | 工具数 | 只读 | 写能力 |
| --- | ---: | ---: | ---: |
| 集群、Namespace 和存储概览 | 5 | 5 | 0 |
| 资源详情、原始 YAML、事件及删除 | 5 | 4 | 1 |
| Namespace 管理 | 3 | 0 | 3 |
| 工作负载管理 | 5 | 2 | 3 |
| YAML 校验和部署 | 3 | 1 | 2 |
| Helm | 6 | 3 | 3 |
| ConfigMap 更新 | 1 | 0 | 1 |
| Pod 日志、Shell、终端和文件 | 8 | 1 | 7 |
| 标准服务、模板和实例配置 | 16 | 10 | 6 |
| Higress 端口台账 | 5 | 2 | 3 |
| **合计** | **57** | **28** | **29** |

## 二、完整工具列表

表中“读”表示 MCP `readOnlyHint=true`；“写”表示受 MCP 写能力开关控制。实际执行仍受业务开关、输入校验、启用的授权/拦截器以及 Kubernetes RBAC 等约束。

### 1. 集群、Namespace 和存储概览：5 个

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `cluster_status` | 读 | 查询集群版本及可见 Namespace 数量 |
| `list_namespaces` | 读 | 列出凭据可访问的 Namespace |
| `list_storage_classes` | 读 | 查询 StorageClass 摘要 |
| `list_resources` | 读 | 查询指定 Namespace 的工作负载、Pod、Service、PVC 等摘要 |
| `list_gateway_resources` | 读 | 查询 GatewayClass、Gateway、HTTPRoute、TCPRoute 摘要 |

### 2. 资源详情、原始 YAML、事件及删除：5 个

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `get_resource` | 读 | 获取资源的结构化详情；部分字段省略或处理，不是原始 YAML |
| `get_resource_yaml` | 读 | 获取支持资源的原始 YAML；Namespace 也支持专门的只读分支 |
| `list_resource_objects` | 读 | 按资源类型和 labels 筛选，分页列出资源对象 |
| `list_events` | 读 | 分页查询 Namespace 事件，可按资源过滤 |
| `delete_resource` | 写 | 删除单个指定资源，带 UID 前置条件；不提供批量删除 |

通用资源类型为：ConfigMap、Pod、Service、PersistentVolumeClaim、Deployment、StatefulSet、DaemonSet、Job、CronJob、Ingress、NetworkPolicy、HorizontalPodAutoscaler、PodDisruptionBudget、Gateway、HTTPRoute、TCPRoute。

Namespace 的创建、删除及元数据更新使用专用工具。GatewayClass 可通过网关概览查询，不代表支持其通用写操作。Secret、RBAC、任意 CRD、任意集群级资源不在这份通用资源白名单中；Secret 原始 YAML 明确拒绝。

### 3. Namespace 管理：3 个

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `create_namespace` | 写 | 创建一个 Namespace |
| `delete_namespace` | 写 | 删除 Namespace 及其内容；保留系统 Namespace 保护检查 |
| `update_namespace_metadata` | 写 | 增加、更新或移除 labels、annotations；不允许修改其他关键字段 |

Namespace YAML 使用 `get_resource_yaml` 的专用分支，概览使用 `list_namespaces`；通用 `get_resource` 不接受 Namespace。

### 4. 工作负载管理：5 个

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `get_rollout_status` | 读 | 获取 Deployment、StatefulSet、DaemonSet 的发布状态快照，不持续等待 |
| `get_workload_history` | 读 | 查询工作负载历史 revision 摘要，不返回历史完整 Pod 模板 |
| `scale_workload` | 写 | 调整 Deployment、StatefulSet 副本数，支持缩容到 0 |
| `restart_workload` | 写 | 对 Deployment、StatefulSet、DaemonSet 发起滚动重启 |
| `rollback_workload` | 写 | 回退到明确的工作负载 revision；调用完成不等于已就绪 |

工作负载的创建、声明式更新和删除分别复用 `apply_manifest`、`delete_resource`，没有重复设置 `create_deployment`、`update_deployment` 等工具。

### 5. YAML 校验和部署：3 个

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `validate_manifest` | 读 | 本地检查 YAML 结构、资源类型及目标范围；不是 Kubernetes Schema 全量校验 |
| `dry_run_manifest` | 写 | 调用 Kubernetes 服务端 dry-run，不持久化，但要求写能力 |
| `apply_manifest` | 写 | 通过 YAML 创建或更新支持的资源，可处理多文档清单 |

apply 不保证多资源事务性，也不自动等待应用 Ready。修改结果未知时应先查询状态，不盲目重复执行。

### 6. Helm：6 个

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `list_helm_releases` | 读 | 查询 Namespace 内 Helm release 摘要 |
| `get_helm_status` | 读 | 查询 release 状态 |
| `get_helm_history` | 读 | 查询 Helm revision 历史摘要 |
| `helm_upgrade` | 写 | 安装或升级 Helm release；目标 Namespace 应已存在，包含制品和资源预检 |
| `helm_rollback` | 写 | 回退到明确的 Helm revision，经过清单检查 |
| `helm_uninstall` | 写 | 卸载 release 及其资源，经过清单检查 |

Helm 查询返回固定摘要，不返回完整 values、manifest、NOTES。当前没有独立的 `helm_template`、`helm_get_values`、`helm_get_manifest` 工具。Helm 写入口禁用 hooks，不开放任意 CLI 参数。

### 7. ConfigMap 更新：1 个

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `update_configmap` | 写 | 向现有 ConfigMap 合并 data 键值；不自动重启应用，不回显 data |

创建或声明式整体更新复用 `apply_manifest`，删除复用 `delete_resource`，原始内容查询使用 `get_resource_yaml`。

### 8. Pod 日志、Shell、终端和文件：8 个

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `get_pod_logs` | 读 | 获取指定 Pod/容器的有限日志；支持上一轮容器日志，不是持续流式订阅 |
| `exec_pod_command` | 写 | 执行一次命令；`action=shell` 时使用容器内 `/bin/sh -c`，也支持固定诊断动作 |
| `open_pod_terminal` | 写 | 创建持续容器终端会话，可开启 TTY |
| `write_pod_terminal` | 写 | 向终端输入命令、换行及控制字符；TTY 下可发送 Ctrl+C |
| `read_pod_terminal` | 写 | 按字节 cursor 读取终端输出，返回二进制安全的 Base64、状态和丢失字节数 |
| `close_pod_terminal` | 写 | 关闭会话并释放连接 |
| `download_pod_file` | 写 | 准备单文件下载，返回下载引用、HTTP 地址和有效期 |
| `upload_pod_file` | 写 | 准备单文件上传，返回上传引用、HTTP 地址和有效期 |

单次执行的固定动作包括：`redis_ping`、`nginx_check`、`disk_usage`、`process_list`、`environment`、`read_file`、`process_status`、`memory_usage`。固定 `read_file` 与任意 shell 的输入语义不同，具体限制以当前启用策略为准。

Shell/终端属于容器 exec，不是 SSH，也不在 k8s-ctl 宿主机执行用户命令。运行身份和目标文件系统权限仍生效；终止连接或发 Ctrl+C 不保证所有远程子进程已经退出。容器等待或崩溃时可能无法进入 Shell，但仍可查询 previous 日志。

文件工具只准备传输，文件正文不经过模型上下文。客户端随后用同一认证凭据通过 HTTP 上传/下载：

- 下载：流式接收单个普通文件，容器需有 tar；当前没有固定大小上限、目录下载或断点续传。
- 上传：发送原始字节及 Content-Length；仅在已有可写目录创建新文件，不覆盖已有文件，校验大小与 SHA-256，容器需具备对应基础命令。
- 引用有效期 5 分钟、单次使用，绑定用户和 Pod UID；服务重启失效。
- 终端为内存会话，受配置的配额、空闲时间和最长存活时间限制；不自动具备多副本共享能力。

### 9. 标准服务、模板和实例配置：16 个

这组能力属于 k8s-ctl 自带 Catalog/SQLite 标准服务目录，不是 publish 的服务配置仓库。

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `list_service_templates` | 读 | 查询不可变模板版本、默认参数和必填参数说明 |
| `get_service_template` | 读 | 查询一个准确模板版本 |
| `save_service_template` | 写 | 新建一个不可变模板版本，不执行部署 |
| `list_standard_services` | 读 | 查询支持的软件、别名、推荐版本和环境默认值 |
| `preview_service_config` | 读 | 渲染标准配置预览，不保存、不预占端口、不修改集群 |
| `list_service_instances` | 读 | 查询 Catalog 实例列表 |
| `get_service_instance` | 读 | 查询实例身份、配置 revision 和部署指针 |
| `save_service_config` | 写 | 新建实例或追加配置 revision，不修改端口或集群 |
| `list_service_revisions` | 读 | 查询不可变配置历史及已渲染 YAML |
| `prepare_service_deployment` | 写 | 冻结配置和 YAML，预占端口，生成 deploymentId；尚不写 Kubernetes |
| `deploy_service` | 写 | 按冻结 deploymentId 部署，包含 dry-run、Higress 端口配置和资源 apply |
| `deploy_standard_service` | 写 | 使用标准默认配置快捷部署，按流程分配外部端口 |
| `get_service_deployment` | 读 | 查询已记录部署及当前 Kubernetes 就绪情况 |
| `verify_service_access` | 读 | 从服务端通过外部 LB 端口检查对应协议，不代表开发者网络一定可达 |
| `list_service_deployments` | 读 | 查询部署历史和冻结制品 |
| `delete_service_instance` | 写 | 删除实例自有资源并按检查结果回收端口；保留 PVC 和共享入口 |

标准软件当前包括 Redis、PostgreSQL/PG、MySQL、MongoDB/Mongo、Milvus、Nginx、Python sandbox。快捷部署要求明确 Namespace；其运行时、存储、镜像及集群依赖需具备。代码支持不表示当前环境已部署这些软件，sandbox 也不表示当前已启用。

### 10. Higress 端口台账：5 个

| MCP 工具名 | 类型 | 功能 |
| --- | --- | --- |
| `list_gateway_ports` | 读 | 查询共享 Higress 外部 TCP 端口池及使用情况 |
| `list_gateway_port_events` | 读 | 查询端口预占、导入、绑定和释放的历史事件 |
| `sync_gateway_ports` | 写 | 读取 Higress Service、Gateway、TCPRoute 后同步 SQLite 台账 |
| `reserve_gateway_port` | 写 | 为已有实例原子预占一个空闲端口 |
| `release_gateway_port` | 写 | 核查实际路由和部署后释放端口；活跃或状态未知时保留 |

仓库本地配置的端口范围为 `30000–30200`，含两端共 201 个，不代表这 201 个端口都已被 LB 实际暴露。外部端口、后端 Service 端口和 NodePort 是不同概念。

## 三、哪些开关决定工具是否可见

| 前提 | 对工具发现的影响 |
| --- | --- |
| `mcp.enabled=false` | 不开放主 MCP 入口；HTTP 可按独立开关运行 |
| 完整模式，关闭 MCP 写能力，关闭 Catalog | 16 个只读工具 |
| 完整模式，关闭 MCP 写能力，开启 Catalog | 28 个只读工具 |
| 完整模式，开启 MCP 写能力，关闭 Catalog 和 Pod exec | 29 个基础 Kubernetes 工具 |
| 完整模式，开启 MCP 写能力，开启 Catalog，关闭 Pod exec | 50 个工具 |
| 上述条件再开启 Pod exec | 增加 `exec_pod_command`，共 51 个 |
| 再开启 Shell，并提供终端能力实现 | 增加 4 个终端工具、2 个文件准备工具，共 57 个 |
| `mcp.diagnostics-only=true` | 保留旧受限模式，只注册日志及满足条件的单次 exec；不注册 Catalog、完整终端或文件工具 |

`kubernetes.write-enabled` 等业务开关决定能否执行，并不等同于注册开关；工具可见不表示调用一定成功。Shell 任意命令还要求显式开启 `shell.enabled`，终端和文件也使用该能力组。文件下载准备当前随写能力组注册，关闭写工具时不会保留它。

当前仓库 `configs/config.yaml` 已开启主 MCP、MCP 写能力、Kubernetes 写能力、Pod exec、Shell 和 Catalog，并关闭 diagnostics-only。该文件只能证明静态配置意图；运行进程可能使用其他配置、覆盖项或旧版本。实际可见工具以目标 MCP 的 `tools/list` 为准。

## 四、与 publish 上层 MCP 的区别

本清单列的是 **k8s-ctl 下层主 MCP 的原始工具名**，通常需要 Namespace、资源类型/名称，容器操作还需要 Pod/容器目标。

publish 对外会封装服务上下文，例如 `k8sctl-get-service-logs`、`k8sctl-exec-service-command` 等，输入 serviceId 和环境，由平台解析集群及 Namespace，再通过 HTTP 调用 k8s-ctl。这些上层名字不能当作下层原始工具名；publish 也不会因为下层实现了 57 个工具就自动全部对外暴露。

同样，k8s-ctl Catalog 的 `deploy_service` 与 publish 的 `deploy-service` 属于不同系统和部署流程，不可仅凭名称相近混用。Chart 列表、Chart 字段定义、values-desc/envs-desc 和服务权限等优先使用 publish 对应能力。

## 五、实现与能力边界

- 已有资源创建/更新通过声明式 YAML 复用，避免为每个 Kind 重复增加 CRUD 工具。
- 没有通用 Secret CRUD、任意 CRD CRUD、节点管理或 kubeconfig 导出工具。
- 没有独立 Helm 模板渲染、完整 release values/manifest 导出工具。
- Pod 日志为有限查询，持续终端输出不等于日志订阅工具。
- 文件字节流走 HTTP，MCP 本身不直接把远程文件保存到开发者电脑。
- 实例内部按已选择的单集群上下文执行；serviceId 到环境/集群的选择由 publish 等上层完成。
- 本清单核对的是实现和注册逻辑；真实环境验证范围查看对应测试报告，不能据数量宣称全部写工具都已在真实集群验收。

## 六、源码和相关文档

- 注册入口：[server.go](../internal/interfaces/mcp/server.go)。
- 资源、工作负载、Helm、Namespace：[operations.go](../internal/interfaces/mcp/operations.go)。
- 单次命令：[pod_exec.go](../internal/interfaces/mcp/pod_exec.go)。
- 终端和文件：[terminal.go](../internal/interfaces/mcp/terminal.go)。
- 标准服务与端口：[catalog.go](../internal/interfaces/mcp/catalog.go)。
- 通用资源类型：[resources.go](../internal/domain/resources.go)。
- 参数与 HTTP 对照：[覆盖矩阵](http-mcp-coverage.md)、[OpenAPI](http-openapi.json)。
- 使用说明：[Shell/终端/文件](shell-mcp.md)、[标准服务目录](standard-services.md)、[MCP 接入测试](mcp-manual-testing.md)。
