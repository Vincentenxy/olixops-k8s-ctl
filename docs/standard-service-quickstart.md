# 常用开发服务

首批支持 Redis、PostgreSQL、MySQL、MongoDB、Milvus、Nginx 和 Python JupyterLab 沙箱。目标是让用户说“在指定 Namespace 部署一个 Redis”，由 MCP 客户端选择服务、补齐参数并调用固定的部署工具，无需手写 Kubernetes YAML。

本批代码新增 `list_standard_services`（只读）与 `deploy_standard_service`（写操作）。PoC MCP 地址：[k8s-ctl-poc](https://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp)。线上需更新镜像后才有新增工具，完整开启共 50 工具。使用方法见 [MCP 指南](mcp-user-guide.md)。

## 首批规格

| software | 别名 | 当前模板 | 镜像 | CPU / 内存 / 磁盘 | 后端端口 |
| --- | --- | --- | --- | --- | --- |
| redis | redis | 2 | redis:7.4.2 | 250m / 256Mi / 1Gi | 6379 |
| postgres | pg、postgresql | 2 | postgres:17.4 | 250m / 512Mi / 5Gi | 5432 |
| mysql | mysql | 1 | mysql:8.4.4 | 250m / 1Gi / 5Gi | 3306 |
| mongodb | mongo | 1 | mongo:8.0.8 | 250m / 512Mi / 5Gi | 27017 |
| milvus | milvus | 1 | milvusdb/milvus:v2.5.11 | 2 / 4Gi / 10Gi | 19530 |
| nginx | nginx | 1 | nginx:1.28-alpine | 100m / 128Mi / 无 PVC | 80 |
| sandbox | python、jupyter、jupyterlab、沙箱、代码沙箱 | 1 | quay.io/jupyter/base-notebook:2025-03-14 | 500m / 1Gi / 临时目录 | 8888 |

运行时以 `list_standard_services` 的实际默认值为准。所有服务为单实例开发规格；数据库使用 StatefulSet、独立 PVC、headless Service 和访问 Service。Nginx/沙箱使用 Deployment、临时存储；升级使用 Recreate。Milvus 使用内嵌 etcd、Rocksmq 和本地存储，不另装 etcd/MinIO。Milvus 按该版本官方 standalone 配置使用 Unconfined seccomp，目标 Namespace 的 PodSecurity 策略需允许该设置。

新模板默认无需 Secret：Redis 无密码、PostgreSQL trust、MySQL root 空密码、MongoDB 无认证，符合当前 PoC 范围。历史 redis:1/postgres:1 保留原凭据要求和内容，不覆盖旧修订。新模板仍可通过 secretName/secretKey 引用已有凭据；Milvus/Nginx/沙箱不接受该凭据参数。PostgreSQL/MySQL 默认创建业务库 app；MongoDB 的 database 参数默认 app，但不会预创建数据库，首次写入时创建。Milvus 使用内置 default 库，Redis 无该数据库参数。MySQL/MongoDB 默认用户名为 root，PostgreSQL 为 app。

## 怎样使用

1. 管理者部署更新后的 k8s-ctl，开启 catalog、MCP 写工具与 Kubernetes 写操作，配置目标 Namespace 白名单及 RBAC。
2. 客户端连接上述 MCP，调用 `list_standard_services` 读取环境默认值与七类服务规格。
3. 用户说“在 wx-test-ns-001 部署一个 MongoDB”，客户端生成唯一 requestId 并调用 `deploy_standard_service`。用户未提供 Namespace 时先提示“请指定部署目标 Namespace”，不能自行选择。
4. 根据返回的 instance.id、deployment.id 继续查询 `get_service_deployment`，并通过 `verify_service_access` 做服务端协议验证。

最小调用（tools/call 的 params）：

```json
{"name":"deploy_standard_service","arguments":{"software":"mongo","namespace":"wx-test-ns-001","requestId":"mongo-demo-001"}}
```

Namespace 是必填字段；可选指定实例名、资源或磁盘：

```json
{"name":"deploy_standard_service","arguments":{"software":"pg","requestId":"pg-app-001","name":"app-db","namespace":"wx-test-ns-001","parameters":{"cpu":"500m","memory":"1Gi","storage":"5Gi"}}}
```

部署 Nginx 并设置首页：

```json
{"name":"deploy_standard_service","arguments":{"software":"nginx","namespace":"wx-test-ns-001","requestId":"nginx-demo-001","parameters":{"content":"<h1>Hello from development</h1>"}}}
```

开一小时 Python 沙箱：

```json
{"name":"deploy_standard_service","arguments":{"software":"sandbox","namespace":"wx-test-ns-001","requestId":"python-demo-001","parameters":{"ttlSeconds":3600}}}
```

沙箱是可在浏览器运行 Python 的 JupyterLab，默认无 token/密码。使用非 root、禁止提权、禁止挂载 ServiceAccount token、丢弃容器 capabilities。它提供容器级开发环境；需要更强隔离可由管理员预装 RuntimeClass，并配置 sandbox-runtime-class 或传 runtimeClassName。模板不设置 hostPath、privileged 或任意控制服务 shell 命令。

默认 TTL 7200 秒，可设 60–86400 秒，从首次部署执行起计算；失败重试和配置修订不续期。到期由控制服务每分钟检查，通过现有 UID 校验删除流程回收工作负载和端口，启动也会扫描过期记录。清理失败保留记录/端口并下轮重试；控制服务停止时不执行回收，恢复运行后补做。文件放临时卷，删除/重建会丢失。

## 环境默认值

`catalog` 配置增加：

```yaml
catalog:
  default-owner: poc
  default-storage-class: local-path
  sandbox-runtime-class: ""
```

可用 `CATALOG_DEFAULT_OWNER`、`CATALOG_DEFAULT_STORAGE_CLASS`、`CATALOG_SANDBOX_RUNTIME_CLASS` 覆盖。Namespace 必须显式提供且在允许范围内，没有默认 Namespace。旧 default-namespace 配置及对应环境变量不再使用。业务层缺少 Namespace 返回 MISSING_NAMESPACE（提示指定 namespace），非法名称返回 INVALID_NAMESPACE，均在保存配置、分配端口和访问集群之前拒绝。持久服务需要实际可用的 StorageClass。Nginx/沙箱不要求 StorageClass/PVC。ownerId 只是归属元数据。软件规格来自固定版本模板，环境配置只补归属元数据、存储与运行时选择。

HTTP 与 MCP 使用同一业务方法：GET `/api/v1/services/standard`，POST `/api/v1/services/deploy-standard`（JSON 为上述 arguments）。HTTP 保留现有 API key 中间件；本批未增加认证流程。

## 返回结果与失败恢复

返回包含 requestId、instance、deployment、protocol、internalAddress、externalAddress。外部端口从配置范围自动领用，保留外部已占用端口；它与数据库原生端口和 NodePort 不同。HTTP 服务的地址带 http://；数据库返回 host:port，不返回密码或连接串。集群 LB 尚未分配地址时不返回外部地址。

请求会复用原有“保存配置 → 冻结部署/领端口 → 检查或创建 Namespace → preflight → dry-run/apply”流程。只有读取明确返回 NotFound 才创建；权限不足、超时、正在删除的 Namespace 会报错。创建冲突 AlreadyExists 会重新读取确认，失败后重复相同请求可继续。Namespace 创建失败保留已准备的部署和端口，不提交工作负载。创建后若存储检查或部署失败，Namespace 保留供重试使用；删除实例也不会删除共享 Namespace。自动创建不自动授予新 Namespace 的业务资源权限；服务账户仍需要对应 RoleBinding 或管理员预先授予的权限，否则后续部署会失败。重复相同 requestId 和原始参数会恢复首次实例/配置/部署，即使环境默认值已变；不同参数复用 requestId 返回 IDEMPOTENCY_CONFLICT。模板升级需新版本，部署新实例需新 requestId。客户端不可在超时后自动改 requestId。失败时用 list_service_revisions/list_service_deployments 查询保留的记录，或原请求重试。

`applied` 表示提交完成，`ready` 表示 Kubernetes 就绪条件满足，均不等于用户网络可达。verify_service_access 分别执行 Redis PING（可选 AUTH）、PostgreSQL SELECT 1、MySQL Ping、MongoDB Ping、Milvus 固定只读集合列表、Nginx/Jupyter HTTP 检查；协议失败不会假称成功。用户侧网络仍需自行访问。删除数据库实例默认保留 PVC，避免误删测试数据；沙箱没有 PVC。

## 本批验收范围

自动化覆盖缺少/非法 Namespace 无副作用、自动创建/已有复用、读取与创建失败、并发 AlreadyExists 回读和 terminating、范围/写开关/授权限制、七类渲染、版本保留、默认值/别名/参数校验、单请求重放、参数冲突、部分提交恢复、并发端口分配、沙箱到期与失败重试、Deployment/StatefulSet 观测及 UID 删除、协议错误响应与 HTTP 重定向边界、HTTP/MCP 工具发现和写开关。测试使用 SQLite 与模拟 Kubernetes/本地协议端点，不代表七个真实软件镜像均已在目标集群启动成功。

Namespace 自动创建与目标 Namespace 内业务资源权限仍需在目标集群验证。目标集群仍应分别完成：镜像拉取、Pod 就绪、持久卷绑定（数据库）、网关转发、协议检查与客户端访问；沙箱还需验证浏览器 Python 执行和到期清理。本次未执行这些真实集群写操作。

配置依据：[MySQL 官方镜像](https://hub.docker.com/_/mysql)、[Jupyter Docker Stacks](https://github.com/jupyter/docker-stacks/blob/main/docs/using/common.md)、[Milvus v2.5.11 standalone 脚本](https://github.com/milvus-io/milvus/blob/v2.5.11/scripts/standalone_embed.sh)。
