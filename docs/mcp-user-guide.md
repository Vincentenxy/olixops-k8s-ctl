# k8s-ctl PoC MCP 使用指南

> 2026-11-06 双入口更新：主 MCP 现有业务工具均有认证 HTTP 对应入口，终端及文件准备接口已补齐；关闭主 MCP 后仍可作为 HTTP 操作器使用。共享契约、最新开关和调用示例见 [HTTP 操作器指南](http-operator-guide.md)，上层多集群路由仍待实现。

> 主认证 MCP 现已支持单次 Shell、持续终端会话和容器文件下载，见 [Shell MCP 使用说明](shell-mcp.md)。这些能力需显式开启 Shell，使用主模式，不使用诊断专用模式。

> **本轮日志与容器诊断手动测试请从 [MCP 接入与手动测试](mcp-manual-testing.md) 开始。** 该文提供本地配置、PAT 和 Codex 导入步骤。下面的公开地址与完整管理工具清单属于旧兼容模式，不适用于开发人员诊断专用实例。

> 2026-11-06 认证更新：已实现可选公司 JWT/PAT 认证。`auth.enabled=true` 时业务接口需要 Bearer，MCP 改为 `/api/v1/mcp`（Higress 下加 `/k8sctl`），旧 `/api/v1/pub/mcp` 不注册。本文原公开入口/API key 或待实现认证描述仅适用于旧兼容模式；资源级授权仍待实现。配置、令牌接口与迁移见 [认证接入说明](authentication.md)。

更新日期：2026-10-26。适用于通过 MCP 客户端或程序调用 Kubernetes 控制服务的 PoC 使用方。

## 1. 服务地址

| 项目 | 配置 |
| --- | --- |
| 推荐连接名称 | `k8s-ctl-poc` |
| MCP URL | [https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp](https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp) |
| 传输方式 | **Streamable HTTP** |
| 健康检查 | [https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/health](https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/health) |
| 就绪检查 | [https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/ready](https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/ready) |
| PoC 认证方式 | 无需认证，不需要配置 Token、API key 或额外认证请求头 |
| 服务名称 / 版本 | `olixops-k8s-ctl` / `0.2.0`，以实际 `initialize` 响应为准 |

路径中的 `/k8sctl` 和 `/api/v1` 都要保留。MCP 客户端应填写完整 MCP URL，不能填写健康检查地址。

当前提供 HTTP API 和 MCP 工具，没有浏览器管理页面。直接在浏览器打开 MCP URL 可能返回 `405 Method Not Allowed`；这不表示 MCP 不可用，应使用支持 Streamable HTTP 的 MCP 客户端连接。

## 2. 快速接入

### 2.1 通用 MCP 客户端

在客户端新增远程 MCP 服务，填写：

```text
名称：k8s-ctl-poc
传输类型：Streamable HTTP / HTTP
URL：https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp
认证：无
```

客户端必须支持 **Streamable HTTP**。不要选择 stdio，也不要填写旧版独立 SSE 的 `/sse` 地址。不同客户端对 HTTP 传输的界面名称可能不同，以是否支持 Streamable HTTP 为准。

保存后重新连接，让客户端获取工具清单。本次代码完整配置提供 **50 个工具：28 个只读工具、22 个写能力工具**（原线上 48 工具版本需升级后生效）。工具清单会随服务配置变化，运行时以 `tools/list` 为准。

### 2.2 Codex 接入

安装 Codex CLI 的使用方，可以执行：

```bash
codex mcp add k8s-ctl-poc \
  --url https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp

codex mcp list
```

也可以在 `~/.codex/config.toml` 中加入以下配置；CLI 和手工配置选择一种即可：

```toml
[mcp_servers.k8s-ctl-poc]
url = "https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp"
```

配置后重新加载 MCP 连接或重新打开会话。在 Codex CLI 的交互界面中，可用 `/mcp` 查看连接状态。

配置方法参考 [OpenAI 官方 MCP 文档](https://learn.chatgpt.com/docs/extend/mcp)。

### 2.3 接入后的第一轮检查

先让客户端依次调用：

| 工具 | 参数 | 用途 |
| --- | --- | --- |
| `list_service_templates` | `{}` | 验证 MCP 调用和标准服务目录读取 |
| `cluster_status` | `{}` | 验证 Kubernetes 连接，查看集群版本 |
| `list_namespaces` | `{}` | 查看可见 Namespace |
| `list_resources` | `{"namespace":"wx-test-ns-001"}` | 验证指定 Namespace 的资源读取 |

可以直接向智能体发送：

> 使用 k8s-ctl-poc，查询集群状态，并列出 wx-test-ns-001 的工作负载、Pod、Service 和 PVC。本次只查询，不修改资源。

## 3. 当前 PoC 联调结果

以下是 2026-10-26 编写本文时的实测快照，不代表所有工具都已完成真实部署验收。

| 检查项 | 结果 |
| --- | --- |
| 通过上述外部 MCP URL 执行 `initialize` | 成功，协商协议版本为 `2025-11-25` |
| 通过上述外部 MCP URL 执行 `tools/list` | 成功，发现 48 个工具 |
| `list_service_templates` | 成功，包含 `redis:1`、`postgres:1` |
| `cluster_status` | 成功，集群版本 `v1.28.0` |
| `list_namespaces` | 成功，当前返回 50 个 Namespace |
| `list_storage_classes` | 成功，当前返回 `local-path` |
| `list_resources`：`wx-test-ns-001` | 成功，可以返回空资源集合 |
| `list_resources`：`wx-test-ns-002` | 成功，能够读取已有资源 |
| `preview_service_config`：Redis 配置预览 | 成功，返回渲染 YAML；没有保存或部署 |
| `list_resources`：`olixops` | 失败，返回通用工具错误，具体原因需服务端核对 |
| 修改、删除、Helm、真实服务部署 | 本次文档验证未执行 |

可见 Namespace 不等于拥有其中所有资源的操作权限。当前示例优先使用已验证可读的 `wx-test-ns-001`、`wx-test-ns-002`；其他 Namespace 和具体写能力需按实际集群权限确认。

## 4. 完整工具清单

下面的名称是 MCP 协议中的工具名。客户端可能显示为 `mcp__k8s_ctl_poc__cluster_status` 等带服务前缀的名称；手工发送 `tools/call` 时，`name` 仍填写 `cluster_status`。

参数名区分大小写，例如 `valuesYaml`、`templateVersion`、`deploymentId`。`—` 表示无参数；调用时传 `{}`。

### 4.1 Kubernetes 与 Helm 查询工具：16 个

| 工具 | 必填参数 | 可选参数 | 用途 |
| --- | --- | --- | --- |
| `cluster_status` | — | — | 查询集群版本与可见 Namespace 数量 |
| `list_namespaces` | — | — | 列出可见 Namespace |
| `list_storage_classes` | — | — | 列出存储类、供给器及回收策略 |
| `list_resources` | `namespace` | — | 查询 Deployment、StatefulSet、Pod、Service、PVC 摘要 |
| `list_gateway_resources` | `namespace` | — | 查询 GatewayClass、Gateway、HTTPRoute、TCPRoute |
| `get_resource` | `namespace, kind, name` | — | 查询单个白名单资源的详情，部分字段经过过滤 |
| `get_resource_yaml` | `kind, name` | `namespace` | 查询原始 YAML；命名空间资源须提供 namespace，Namespace 对象可省略 |
| `list_resource_objects` | `namespace, kind` | `labelSelector, limit, continue` | 按资源类型、标签分页查询详情 |
| `list_events` | `namespace` | `kind, name, limit, continue` | 查询事件，可按对象过滤 |
| `get_pod_logs` | `namespace, pod` | `container, tailLines, sinceSeconds, previous, timestamps` | 查询 Pod 容器日志，不持续跟随 |
| `get_rollout_status` | `namespace, kind, name` | — | 查询 Deployment、StatefulSet、DaemonSet 的就绪快照 |
| `get_workload_history` | `namespace, kind, name` | `limit, continue` | 查询工作负载可用历史版本号 |
| `list_helm_releases` | `namespace` | `limit, offset` | 查询 Namespace 内的 Helm release 摘要 |
| `get_helm_status` | `namespace, release` | — | 查询指定 release 状态 |
| `get_helm_history` | `namespace, release` | `limit` | 查询指定 release 的 revision 历史 |
| `validate_manifest` | `namespace, manifest` | — | 本地检查 YAML、资源白名单及 Namespace，不访问 Kubernetes |

### 4.2 Kubernetes 与 Helm 操作工具：13 个

这些工具需要服务端开启写能力，并具备相应集群权限。`dry_run_manifest` 虽不持久化资源，也属于写能力工具。

| 工具 | 必填参数 | 可选参数 | 用途与效果 |
| --- | --- | --- | --- |
| `dry_run_manifest` | `namespace, manifest` | `serverSide` | Kubernetes 服务端 dry-run 校验，不保存资源 |
| `apply_manifest` | `namespace, manifest` | `serverSide` | 创建或更新 YAML 中的白名单资源 |
| `helm_upgrade` | `namespace, release, chart` | `version, valuesYaml, wait` | 安装或升级 Helm release |
| `scale_workload` | `namespace, kind, name, replicas` | — | 设置 Deployment / StatefulSet 副本数，允许 0 |
| `restart_workload` | `namespace, kind, name` | — | 请求 Deployment / StatefulSet / DaemonSet 滚动重启 |
| `rollback_workload` | `namespace, kind, name, revision` | — | 将工作负载回滚到指定历史版本 |
| `delete_resource` | `namespace, kind, name` | — | 删除单个白名单资源，不支持批量删除 |
| `helm_rollback` | `namespace, release, revision` | `wait` | 将 Helm release 回滚到指定 revision |
| `helm_uninstall` | `namespace, release` | `wait` | 卸载 Helm release 及其关联资源 |
| `create_namespace` | `namespace` | — | 创建 Namespace；不会自动获得新 Namespace 的工作负载权限 |
| `delete_namespace` | `namespace` | — | 删除整个 Namespace 及其内容；不允许 default 和 kube-* |
| `update_namespace_metadata` | `namespace` | `labels, annotations, removeLabels, removeAnnotations` | 增改或显式移除 Namespace 标签、注解，不修改其他字段 |
| `update_configmap` | `namespace, name, data` | — | 合并更新已有 ConfigMap 的 data 键，不自动重启工作负载 |

### 4.3 标准服务目录查询工具：12 个

标准服务目录提供 Redis、PostgreSQL、MySQL、MongoDB、Milvus、Nginx、Python 沙箱模板、独立实例、配置修订、部署记录与共享 TCP 端口管理。

| 工具 | 必填参数 | 可选参数 | 用途 |
| --- | --- | --- | --- |
| `list_standard_services` | — | — | 查询七类服务、别名、推荐版本和环境默认值 |
| `list_service_templates` | — | — | 查询全部模板版本、默认参数和参数 Schema |
| `get_service_template` | `software, version` | — | 查询某个软件的指定模板版本 |
| `preview_service_config` | 见下文 `CONFIG_INPUT` | `instanceId, requestId` | 预览合并后的配置和 YAML，不保存、不占端口、不部署 |
| `list_service_instances` | — | `ownerId, instanceId, limit, offset` | 查询已保存的实例 |
| `get_service_instance` | `instanceId` | — | 查询实例身份、当前配置修订及部署关联 |
| `list_service_revisions` | — | `ownerId, instanceId, limit, offset` | 查询不可变配置修订及原始 YAML |
| `list_service_deployments` | — | `ownerId, instanceId, limit, offset` | 查询保存的部署历史与最终 YAML |
| `get_service_deployment` | `deploymentId` | — | 查询部署记录和当前 Kubernetes 就绪状态 |
| `verify_service_access` | `deploymentId` | — | 从服务端经外部 LB 端口验证所选软件的固定只读协议 |
| `list_gateway_ports` | — | `port, state, limit, offset` | 查询共享 TCP 端口池、占用者与路由映射 |
| `list_gateway_port_events` | — | `port, state, limit, offset` | 查询端口同步、领用、绑定、释放历史 |

### 4.4 标准服务目录操作工具：9 个

| 工具 | 必填参数 | 可选参数 | 用途与效果 |
| --- | --- | --- | --- |
| `deploy_standard_service` | `software, namespace, requestId` | `name, ownerId, templateVersion, parameters` | 必须指定 Namespace，不存在时自动创建；其他参数使用默认配置、自动分配端口；失败沿用同一 requestId 重试 |
| `save_service_template` | `software, version, defaults` | `manifestTemplate` | 保存新模板版本，不覆盖已有版本，不部署 |
| `save_service_config` | `CONFIG_INPUT` 加 `requestId` | `instanceId` | 新建实例或追加配置修订，不占端口、不部署 |
| `sync_gateway_ports` | — | — | 读取共享入口实际占用并更新端口台账，不修改 Kubernetes |
| `reserve_gateway_port` | `instanceId, port, requestId` | `note` | 为已有实例预留一个外部端口，不部署 |
| `release_gateway_port` | `port, requestId` | — | 检查实际路由和部署状态后释放端口 |
| `prepare_service_deployment` | `instanceId, revision, port, requestId` | — | 固定配置修订、预留端口并生成 deploymentId，不部署 Kubernetes 资源 |
| `deploy_service` | `deploymentId` | — | 对准备好的部署执行 dry-run、共享入口维护和实例资源 apply |
| `delete_service_instance` | `instanceId, requestId` | — | 删除实例受管资源并回收符合条件的端口；保留 PVC 和共享入口 |

`CONFIG_INPUT` 的字段如下：

| 字段 | 类型 | 使用说明 |
| --- | --- | --- |
| `ownerId` | string | 实例归属标记，如 `team-a`；PoC 中仅作为元数据 |
| `name` | string | 实例名称，DNS label 格式，最长 36 个字符 |
| `namespace` | string | 实例部署的 Namespace |
| `software` | string | `redis` 或 `postgres` |
| `templateVersion` | integer | 模板版本，先查询工具确认，例如 `1` |
| `parameters` | object | 覆盖模板默认值，字段见下表 |
| `expectedRevision` | integer | 新实例传 `0`；更新时传当前配置修订号 |
| `instanceId` | string，可选 | 更新已有实例时传入；新实例不填 |
| `requestId` | string | 保存时业务必填；预览时可省略 |

`save_service_config` 的发现 Schema 将 `requestId` 标为可选，但业务保存会校验非空，因此**保存时必须提供**。重新提交同一次保存时复用相同 requestId 和相同参数；修改参数后使用新 requestId。

`parameters`、模板 `defaults` 使用以下字段。未覆盖的值由所选模板补齐，最终必填项以模板返回的 `parameterSchema` 为准。

| 字段 | 含义 | 示例 |
| --- | --- | --- |
| `image` | 容器镜像 | `redis:7.4.2`、`postgres:17.4` |
| `cpu` | CPU 请求和限制 | `250m` |
| `memory` | 内存请求和限制 | `256Mi`、`512Mi` |
| `storage` | PVC 容量 | `1Gi`、`5Gi` |
| `storageClass` | 目标集群的存储类 | 当前实测有 `local-path` |
| `secretName` | 已有凭据 Secret 名称 | `redis-guide-auth`，示例名称，部署前须确认存在 |
| `secretKey` | Secret 中的密码键名 | `password` |
| `database` | PostgreSQL 数据库名 | `app` |
| `username` | PostgreSQL 用户名 | `app` |

工具参数只传 Secret 引用，不传密码原文。本文不涉及凭据资源的创建流程。

## 5. 常用调用示例

以下 JSON 都是 `tools/call` 的 `params` 内容，可在 MCP 调试客户端中填入，或由智能体按工具 Schema 调用。第 7 节提供完整 HTTP 请求。

### 5.1 查询 Namespace 资源

```json
{
  "name": "list_resources",
  "arguments": {"namespace": "wx-test-ns-001"}
}
```

空 Namespace 可能返回 `null` 或空资源列表，属于成功结果，不表示调用失败。

### 5.2 按类型和标签查询

```json
{
  "name": "list_resource_objects",
  "arguments": {
    "namespace": "wx-test-ns-002",
    "kind": "Pod",
    "labelSelector": "app=example",
    "limit": 20
  }
}
```

`app=example` 是筛选示例，需替换为实际标签。响应中有非空 `continue` 时，下一次调用原样带回该值。

### 5.3 查询日志

先通过资源列表得到实际 Pod 名称，再调用：

```json
{
  "name": "get_pod_logs",
  "arguments": {
    "namespace": "wx-test-ns-002",
    "pod": "替换为实际Pod名称",
    "tailLines": 100,
    "sinceSeconds": 600,
    "timestamps": true
  }
}
```

多容器 Pod 可补充 `container`。查看上一个容器实例时设置 `previous: true`。

### 5.4 预览 Redis 配置

以下调用已验证可以渲染；它不会保存实例、预留端口或创建集群资源：

```json
{
  "name": "preview_service_config",
  "arguments": {
    "ownerId": "poc-demo",
    "name": "redis-guide",
    "namespace": "wx-test-ns-001",
    "software": "redis",
    "templateVersion": 1,
    "parameters": {
      "storageClass": "local-path",
      "secretName": "redis-guide-auth"
    },
    "expectedRevision": 0
  }
}
```

预览成功不代表引用的 Secret、存储或共享入口已经具备部署条件。预览返回的实例 ID 不代表已保存实例；后续步骤使用 `save_service_config` 真正返回的 ID。

### 5.5 扩缩容示例

仅在需要变更目标工作负载时调用。`example-app` 必须替换为实际存在的 Deployment 名称。

```json
{
  "name": "scale_workload",
  "arguments": {
    "namespace": "wx-test-ns-001",
    "kind": "Deployment",
    "name": "example-app",
    "replicas": 2
  }
}
```

随后调用 `get_rollout_status` 查询结果。`accepted: true` 只表示请求被接受，不等于所有副本已经就绪。

## 6. 标准 Redis / PostgreSQL 服务使用顺序

| 步骤 | 调用工具 | 需要关注的结果 |
| --- | --- | --- |
| 1. 查询模板 | `list_service_templates` / `get_service_template` | 确认软件、模板版本、默认值与参数 Schema |
| 2. 预览配置 | `preview_service_config` | 检查合并参数与渲染 YAML |
| 3. 保存配置 | `save_service_config` | 保存步骤 2 的输入并补充 requestId，取得 instance.id、revision.revision |
| 4. 同步入口占用 | `sync_gateway_ports`，再调用 `list_gateway_ports` | 根据实时台账选择可分配端口；同步会更新台账 |
| 5. 准备部署 | `prepare_service_deployment` | 传 instanceId、revision、port、requestId，取得 deploymentId |
| 6. 执行部署 | `deploy_service` | 传 deploymentId，服务端提交固定的配置与资源 |
| 7. 查询就绪 | `get_service_deployment` | 查看工作负载、PVC、Gateway、Route 等实际状态 |
| 8. 验证协议访问 | `verify_service_access` | 查看服务端经 LB 端口的 TCP、Redis / PostgreSQL 协议验证结果 |

准备部署的参数示例：

```json
{
  "name": "prepare_service_deployment",
  "arguments": {
    "instanceId": "替换为保存返回的instance.id",
    "revision": 1,
    "port": 30032,
    "requestId": "poc-redis-deploy-001"
  }
}
```

`revision: 1` 和 `port: 30032` 都是示例值，以实际保存结果和实时端口台账为准。标准准备操作会预留端口，通常不需要额外调用 `reserve_gateway_port`。

执行部署、查询部署和验证协议使用同一个 deploymentId：

```json
{"name":"deploy_service","arguments":{"deploymentId":"替换为准备返回的id"}}
```

```json
{"name":"get_service_deployment","arguments":{"deploymentId":"替换为准备返回的id"}}
```

```json
{"name":"verify_service_access","arguments":{"deploymentId":"替换为准备返回的id"}}
```

使用过程中的几个约定：

- 配置修订从 `1` 开始；更新已有实例时，`expectedRevision` 使用当前修订号。
- 更新配置不会自动部署；部署始终引用一个明确的修订号。
- 端口是外部入口端口，区别于 Redis 的 `6379`、PostgreSQL 的 `5432` 和 Kubernetes NodePort。
- 执行超时或连接中断后，先查询原 deploymentId。需要重试时复用同一个 deploymentId，不另建部署记录。
- 配置保存、准备部署、手工领用和释放的重复请求，须复用各自动作原有 requestId 和原参数。
- `get_service_deployment` 的 `observation` 反映本次查询的即时状态，`phase` 是保存的执行状态；apply 成功不等于服务可用。
- `verify_service_access` 只验证控制服务所在网络的访问，使用方仍需从自己的网络实际连接。
- `delete_service_instance` 保留 PVC；`delete_resource`、`delete_namespace` 和 Helm 卸载没有相同的数据保留承诺。

## 7. 使用 curl 直接联调

### 7.1 健康检查

```bash
curl -fsS \
  https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/health
```

健康接口返回 REST 格式，例如：

```json
{"code":0,"msg":"success","data":{"service":"olixops-k8s-ctl","status":"ok","version":"0.2.0"}}
```

健康和就绪接口当前只能证明 HTTP 进程可用，不能代替 Kubernetes 工具调用验证。

### 7.2 初始化 MCP

```bash
curl -fsS \
  https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data-binary '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"poc-manual-test","version":"1.0"}}}'
```

当前实测响应：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "capabilities": {"logging": {}, "tools": {"listChanged": true}},
    "protocolVersion": "2025-11-25",
    "serverInfo": {"name": "olixops-k8s-ctl", "version": "0.2.0"}
  }
}
```

随后发送初始化完成通知：

```bash
curl -fsS \
  https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  --data-binary '{"jsonrpc":"2.0","method":"notifications/initialized"}'
```

通知没有 JSON-RPC id，也没有 JSON-RPC 结果响应；空响应体正常。生产客户端应自动完成初始化与版本协商。下面示例使用当前协商结果；服务升级后以 initialize 返回的 `protocolVersion` 为准。

### 7.3 读取工具清单

```bash
curl -fsS \
  https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  --data-binary '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

结果中的 `result.tools` 包含工具名、说明、`inputSchema`、读写标注等信息。

### 7.4 调用一个只读工具

```bash
curl -fsS \
  https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  -H 'X-Request-Id: poc-guide-cluster-001' \
  --data-binary '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"cluster_status","arguments":{}}}'
```

查询指定 Namespace 时替换请求体为：

```json
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_resources","arguments":{"namespace":"wx-test-ns-001"}}}
```

本服务使用无持久会话的 Streamable HTTP，当前不需要保存或回传 `Mcp-Session-Id`。

### 7.5 判断调用结果

- 健康检查属于 REST，读取 `code`、`msg`、`data`。
- MCP 使用 JSON-RPC，读取 `result.content`，部分工具还返回 `result.structuredContent`。
- `result.isError: true` 表示工具执行失败，即使 HTTP 状态是 `200` 也不能当作成功。
- 顶层 `error` 表示 JSON-RPC / MCP 层错误，例如参数格式错误或工具不存在。
- 命令中的 `curl -f` 只能发现 HTTP 错误，不能代替检查 MCP 的 `isError`。

`X-Request-Id` 用于请求排查，服务会生成或透传并回写到响应头。它不同于业务幂等字段 `requestId`，也不同于 JSON-RPC 的 `id`。

## 8. 使用范围与参数约定

| 项目 | 约定 |
| --- | --- |
| 目标集群 | 服务端已配置的单个集群；调用方不传 kubeconfig 或集群连接地址 |
| Namespace | 业务参数中显式指定；是否可读写取决于服务配置和集群权限 |
| 工作负载 kind | 使用 `Deployment`、`StatefulSet`、`DaemonSet` 等准确名称；扩缩容仅前两种 |
| Kubernetes 分页 | 默认 `limit=100`，最大 `200`；响应有非空 continue 时继续读取 |
| Helm 分页 | 使用 `limit` 和 `offset` |
| 目录分页 | 使用 `limit` 和 `offset`，默认 100、上限 500 |
| 日志范围 | 默认最近 200 行，最多 5000 行；sinceSeconds 不超过 7 天 |
| 日志大小 | 最多读取 256 KiB，检查返回的 truncated |
| 副本数 | 整数 `0` 至 `10000`；设为 0 会停止工作负载实例 |
| 回滚版本 | `revision` 必须是明确的正整数 |
| 请求大小 | MCP 请求体最多 4 MiB；manifest、Helm values 各最多 2 MiB |
| 执行时间 | 读工具默认最多 30 秒；写工具默认总时限 120 秒，以服务配置为准 |

工作负载历史的某一页可能为空但仍带 continue，不能仅凭空列表判定分页结束。Helm、目录和 Kubernetes 工具的输出形态不同，以对应工具实际响应为准。

通用资源查询、apply 和删除支持以下白名单类型：

- `ConfigMap`、`Pod`、`Service`、`PersistentVolumeClaim`。
- `Deployment`、`StatefulSet`、`DaemonSet`。
- `Job`、`CronJob`、`Ingress`、`NetworkPolicy`。
- `HorizontalPodAutoscaler`、`PodDisruptionBudget`。
- `Gateway`、`HTTPRoute`、`TCPRoute`，还需集群提供对应 API 版本。

Namespace 使用专用工具管理；`get_resource_yaml` 支持通过 kind=Namespace 查看指定 Namespace。Secret、RBAC、CRD 不在通用资源操作白名单内。不提供任意 shell 命令、Pod exec、kubeconfig 导出或节点管理工具。

Helm 的 `chart` 接受已配置仓库的 `repo/chart` 或 `oci://registry/path`，不接受本地路径、任意 HTTP URL 或额外命令参数。建议显式指定 version。当前关闭 hooks，不自动创建 Namespace 或安装 CRD；包含超出白名单资源的 Chart 会被拒绝。升级不会隐式继承上次覆盖值，需要传入本次要保留的 valuesYaml。

多文档 apply、Helm 变更和标准服务部署不保证失败时自动回滚。超时、中断或返回错误后先查询实际状态，再决定重试。回滚工作负载或 Helm 配置不会恢复数据库数据。

## 9. 常见问题

| 现象 | 处理方法 |
| --- | --- |
| 浏览器打开 MCP 返回 405 | 使用 Streamable HTTP 客户端；手工联调用 POST JSON-RPC |
| 健康地址返回 404 | 检查是否缺少 `/k8sctl` 或 `/api/v1` |
| 工具清单不完整 | 重新连接并执行 tools/list；服务可能关闭了写工具或标准服务目录 |
| 模板可查，集群工具失败 | MCP 连接已通；需核对目标 Namespace、服务端连接和集群权限 |
| Namespace 可见，但里面资源无法查询 | Namespace 列表权限和工作负载权限不同；切换到已获授权的 Namespace |
| 资源列表成功但没有对象 | 目标 Namespace 可能为空，或标签筛选没有匹配 |
| HTTP 200，但客户端显示失败 | 检查 result.isError、content 和顶层 error |
| 返回 `check input, feature switches, configured scope, policy hook and cluster availability` | 这是通用错误；向服务维护方提供工具名、目标 Namespace、发生时间和 X-Request-Id |
| `REVISION_CONFLICT` | 重新读取实例当前修订，再提交正确的 expectedRevision |
| `IDEMPOTENCY_CONFLICT` | 同一个 requestId 对应不同参数；原请求重试保持原参数，新操作使用新 ID |
| `GATEWAY_SYNC_INCOMPLETE` | 共享入口未完成核对；由维护方检查网关读取条件，再调用 sync_gateway_ports |
| `PORT_IN_USE` / `PORT_NOT_IN_POOL` | 读取实时端口池，选择有效且可分配的端口 |
| `DEPLOYMENT_RESULT_UNKNOWN` 或调用超时 | 查询原 deploymentId，核对已有资源，必要时重试同一部署 |
| `DELETE_PENDING` | 资源或路由尚未完全删除，检查状态后复用原删除请求重试 |

## 10. 可直接发给智能体的示例

**查询集群：**

> 使用 k8s-ctl-poc 查询集群版本、可见 Namespace 和 StorageClass，只查询。

**排查工作负载：**

> 使用 k8s-ctl-poc 检查 wx-test-ns-002 中的 Pod 和 StatefulSet。对未就绪对象查询事件和最近 100 行日志，汇总原因，本次不重启、不修改。

**预览标准 Redis：**

> 使用 k8s-ctl-poc 读取 Redis 模板版本 1，为 wx-test-ns-001 预览名为 redis-guide、ownerId 为 poc-demo 的配置。storageClass 使用 local-path，Secret 引用 redis-guide-auth，只预览，不保存、不分配端口、不部署。

**执行指定变更：**

> 使用 k8s-ctl-poc 将 wx-test-ns-001 中名为实际工作负载名称的 Deployment 调整为 2 副本，并查询 rollout 状态。请先确保名称对应目标对象。

实际工具名称、参数 Schema 和返回结构，以当前连接的 `tools/list` 和工具响应为准。


## 常用服务的简化入口（本次新增）

参见 [常用服务使用说明](standard-service-quickstart.md)，包含七类服务规格、最小请求、沙箱 TTL 与参数覆盖。先用 `list_standard_services` 读取环境默认值，再由 `deploy_standard_service` 一次完成保存、准备与提交部署。namespace 必填，未指定时先提示用户补充；指定但不存在则自动创建（须符合范围和权限），不新增 autoCreateNamespace 入参。旧的多步骤工具全部保留。

```json
{"name":"deploy_standard_service","arguments":{"software":"milvus","namespace":"wx-test-ns-001","requestId":"milvus-demo-001"}}
```

上文“2026-10-26 真实环境验证”的 48 工具结果属于升级前线上版本，不能作为新增工具或七类服务真实运行的验收证据。新增模板/入口已做自动化测试；仍需在目标集群升级并验证镜像、存储和协议访问。


## 可选的容器诊断桥接

Kubernetes MCP Server v0.1.2 的私有 sidecar 接入、`exec_pod_command`、REST 日志/执行入口及配置/RBAC 见 [接入说明](kubernetes-mcp-integration.md)。默认关闭，启用后在原有工具集上增加一个写工具；已有 `get_pod_logs` 复用，不重复注册上游工具。客户端继续使用 k8s-ctl 的 JWT/PAT 认证入口，不直连 sidecar。


开发人员使用的两个诊断工具、按用户精确授权、managed 本地 MCP 验证和文件读取新增边界，见 [开发人员 MCP 诊断](developer-diagnostics.md)。该模式与完整管理工具集分开部署。


## 容器文件保存到开发人员本地

使用同一个主 MCP 的 `download_pod_file(namespace, pod, container, path)` 准备下载，工具返回 downloadId、downloadUrl、method、name、expiresAt、podUID；随后由本地智能体或客户端用个人 PAT 调用 `POST /api/v1/cluster/pods/files/download`，JSON 请求体为 `{"downloadId":"..."}`，用 curl 保存原始二进制文件。成功响应是文件流，开始传输前错误仍使用统一 JSON envelope。

下载不设固定大小上限，引用 5 分钟有效、用户绑定、一次使用；不支持 Range/断点续传。目标容器需要 tar，可读取 ConfigMap 符号链接指向的普通文件；无需 Perl 或客户端 kubeconfig。部署经过 Higress `/k8sctl` 时，在配置文件设置 server.public-url 为实际外部地址并保留此前缀。curl 完整示例及本地辅助客户端见 [Shell MCP 使用说明](shell-mcp.md)，并发、大文件等后续事项见 [开发待办](roadmap.md)。


上传使用主 MCP 的 `upload_pod_file(namespace,pod,container,path)` 准备，然后 curl 携带 PAT、`Content-Type: application/octet-stream`、`X-Upload-Id`，向返回的 uploadUrl 执行 POST，`--data-binary @本地文件`。首版只创建新文件，父目录必须已有且可写；不覆盖已有内容、不自动 reload。成功返回统一 JSON（path、size、sha256、podUID）。grants 非空时需独立 `upload_file` 动作。完整示例与失败语义见 [Shell MCP 使用说明](shell-mcp.md)。
