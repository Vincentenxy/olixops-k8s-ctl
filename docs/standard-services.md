# 标准服务目录、配置和端口台账

更新：2026-10-26。已实现代码和模拟集群验收，尚未向真实集群创建实例或修改 Higress。只需要部署 k8s-ctl；SQLite 是进程内数据库，无需另部署数据库服务，olixops-agent 本轮无需变更。

## 模块与阅读路径

```text
HTTP handler/catalog.go ─┐
                        ├→ application/catalog.Service → domain/catalog.Repository
MCP mcp/catalog.go ──────┘             │                            ↓
                                     │                     catalogdb.Store (SQLite)
                                     ↓
                           domain/catalog.Cluster
                                     ↓
                        application.CatalogCluster
                          │                   │
                  ClusterService         kubernetes/catalog*.go
                scope + policy + apply   Gateway / inventory / UID / readiness / protocol
                          ↓                   ↓
                   Provider + kubectl     Provider + client-go
```

- `internal/domain/catalog/model.go`：模板、实例、修订、部署、端口、事件，以及存储/集群契约。
- `internal/infrastructure/catalogdb/sqlite.go`：纯 Go SQLite 驱动，`CGO_ENABLED=0` 可构建；迁移、主键约束、事务、WAL 和关闭。
- `internal/application/catalog/templates.go`：初始模板、参数规则、固定默认值与渲染。
- `internal/application/catalog/service.go`：实例身份、乐观锁、不可变修订、幂等和共享权限接入点。
- `internal/application/catalog/ports.go`：台账同步、事务领用、释放和历史。
- `internal/application/catalog/deployments.go`：准备、执行、中断恢复、回读、删除与访问验证。
- `internal/application/catalog_cluster.go`：复用现有 Namespace/GVK 检查、Authorizer、写开关和 apply。
- `internal/infrastructure/kubernetes/catalog*.go`：实际 Kubernetes 适配，与业务流程隔离。

SQLite 使用六张业务表：`service_templates`、`service_instances`、`service_revisions`、`service_deployments`、`gateway_ports`、`port_events`，另有 `schema_migrations`。每张业务表按稳定 ID 存 JSON payload，端口主键为 `gatewayId:TCP:port`。初始化不覆盖已有模板；版本大于程序支持的迁移版本会拒绝启动。

当前适用于一个进程、单副本和较小配置目录。每次事务加载目录聚合，按主键增量 upsert；`BEGIN IMMEDIATE`、busy timeout 和 SQL 主键保证多个连接不会重复领用。网络调用均在事务外。后续数据量增长应改为按实体查询和索引，不将其作为高并发配置中心。**单 SQLite 文件只能有一个正在执行业务的服务进程**；不能横向扩容部署协调器，避免不同进程的执行锁失效。

## 配置与持久化

`configs/config.yaml` 已开启目录，开发默认 `./data/olixops.db`。Docker 使用 `/data/olixops.db`，目录须挂载 PVC。首次启动只初始化模板和端口，**不会扫描或修改集群**。

```yaml
catalog:
  enabled: true
  database-path: ./data/olixops.db
  cluster: configured
  gateway-id: higress
  gateway-namespace: higress-system
  gateway-service: higress-gateway
  gateway-name: higress-tcp-gateway
  port-start: 30000
  port-end: 30200
```

每项可用对应环境变量覆盖，例如 `CATALOG_ENABLED`、`CATALOG_DATABASE_PATH`、`CATALOG_PORT_START`。同一 gateway-id 的集群、网关与范围绑定在数据库中；更换入口须恢复原配置或使用新 ID，旧部署不能拿新入口执行。当前 API 面向一个已配置入口，不支持请求指定连接地址。

[deployments/catalog.yaml](../deployments/catalog.yaml) 提供单副本、Recreate、PVC、非 root UID/GID 10001 和 InCluster 示例，不自动执行。先准备 `olixops` Namespace、专用 ServiceAccount/RBAC、`olixops-control` Secret 的 `api-key`。示例数据库 PVC 使用集群默认 StorageClass，需要时显式指定。

数据库文件权限 0600，目录新建时 0700；已加入 Git 和 Docker context 忽略。备份时先停止进程，再一起保存数据库及 WAL/SHM，或采用 SQLite 一致性备份工具；不要只复制运行中的主文件。不要清空 SQLite 来“修复”端口占用。

## 标准模板与参数

内置 `redis:1` 与 `postgres:1`，实际软件镜像分别为示例固定版本 `redis:7.4.2`、`postgres:17.4`，不会追踪 latest 或自动升级。上线前按团队批准的版本保存新模板。每个实例包含独立名字的 StatefulSet、ClusterIP Service、ConfigMap、显式 PVC，部署时另生成 TCPRoute。实例资源名为 `name-<随机ID前8位>`，归属使用完整 instanceId 标签，不靠名字猜用户。

| 参数 | Redis 默认 | PostgreSQL 默认 | 说明 |
| --- | --- | --- | --- |
| image | redis:7.4.2 | postgres:17.4 | 必须带 tag/digest |
| cpu | 250m | 250m | requests 与 limits 相同，正 Kubernetes quantity |
| memory | 256Mi | 512Mi | requests 与 limits 相同 |
| storage | 1Gi | 5Gi | 显式 PVC，可增大，不可缩小 |
| storageClass | 无 | 无 | 必填，部署前检查存在 |
| secretName | 无 | 无 | 必填，同 Namespace 的已有 Secret |
| secretKey | password | password | 非空密码键 |
| username / database | 不使用 | app / app | 简单标识符 |

Redis 后端固定 6379、AOF 持久化，环境变量引用 Secret 供 requirepass。PostgreSQL 后端固定 5432，PGDATA 为 `/var/lib/postgresql/data/pgdata`。配置通过 ConfigMap 挂载，StatefulSet Pod template 记录修订号，更新修订会触发滚动更新。探针检查 TCP 就绪；业务协议另用 verify-access 验证。第一版数据库连接为未配置 TLS 的模板。

凭据由已有 Secret 注入，不提供自动创建 Secret，不把密码存进模板或参数。`secretName` 可以更新，但 PostgreSQL 的初始化环境变量不会替已有数据库修改密码；必须先完成数据库内凭据轮换，再改引用，不能把改引用当作改密码。

已有实例禁止改变 image、StorageClass、PostgreSQL username/database，以及 ownerId/name/Namespace/software。数据库版本迁移另行设计；新模板可用于新实例。模板不可覆盖同版本，运行时从 SQLite 读取确定版本；自定义模板只能包含受限的 StatefulSet/Service/PVC/ConfigMap，最终 TCPRoute 由协调器生成，不能带 Secret 或共享入口对象。

## 智能体调用顺序

1. `list_service_templates` / `get_service_template`：获得 schema、默认参数，向用户补齐 Namespace、StorageClass、Secret 引用。
2. `preview_service_config`：本地渲染预览，不写数据库或集群。
3. `save_service_config`：创建独立实例或保存新配置修订；不会占端口。
4. `sync_gateway_ports` / `list_gateway_ports`：核对共用入口。30000–30200 **含两端，共 201 个端口**；外部端口与进程端口、NodePort 分开。
5. `prepare_service_deployment`：固定 instanceId、revision、port、requestId，事务预留并返回 deploymentId、最终 YAML 和 SHA-256 摘要。
6. `deploy_service(deploymentId)`：不再让智能体拼 YAML。检查 Namespace、StorageClass、Secret、资源归属，server dry-run，增量维护共享 Service/Listener，再 apply 实例 YAML。
7. `get_service_deployment`：查询实际 Pod/StatefulSet、PVC、Gateway、Listener 和 TCPRoute 条件，轮询至就绪。
8. `verify_service_access`：从控制服务经 LB 端口执行 Redis AUTH/PING 或 PostgreSQL SELECT 1，分别返回 TCP/协议结果。用户网络始终未验证；需用户从自身网络连接确认。

实例创建和配置保存的 `requestId` 共用同一幂等空间；准备、手工领用、释放分别在各自动作空间幂等。重试必须复用相同 requestId 和相同参数，改变参数须用新 ID。更新配置必须传当前 `expectedRevision`，修订从 1 开始。配置版本与部署版本可以不同，部署始终绑定明确修订。

## HTTP 与 MCP 对照

REST 均在 `/api/v1`，沿用 `X-Olixops-Control-Key`。无参数查询 GET，带参数查询/操作 POST，不使用业务 PUT/DELETE。POST 请求为 JSON。列表输入通常为 `instanceId? ownerId? limit? offset?`，默认 100、上限 500；端口 GET 和 sync 返回完整池。

| HTTP 方法与路径 | MCP 工具 | 属性 |
| --- | --- | --- |
| GET /templates | list_service_templates | 读 |
| POST /templates/get | get_service_template | 读 |
| POST /templates/save | save_service_template | 写 SQLite |
| GET /services；POST /services/list | list_service_instances | 读 |
| POST /services/get | get_service_instance | 读 |
| POST /services/preview-config | preview_service_config | 读 |
| POST /services/save-config | save_service_config | 写 SQLite |
| POST /services/revisions | list_service_revisions | 读 |
| POST /services/prepare-deployment | prepare_service_deployment | 写 SQLite |
| POST /services/deploy | deploy_service | 写 SQLite/Kubernetes |
| POST /services/deployment | get_service_deployment | 读数据库/集群 |
| POST /services/deployments | list_service_deployments | 读数据库 |
| POST /services/verify-access | verify_service_access | 读集群、网络协议验证 |
| POST /services/delete | delete_service_instance | 写 SQLite/Kubernetes |
| GET /ports；POST /ports/list | list_gateway_ports | 读 |
| POST /ports/sync | sync_gateway_ports | 写 SQLite，读集群 |
| POST /ports/reserve | reserve_gateway_port | 写 SQLite |
| POST /ports/release | release_gateway_port | 写 SQLite，读集群 |
| POST /ports/events | list_gateway_port_events | 读 |

新增 **11 个读工具、8 个写工具**。目录开启时总计 27 读 + 21 写 = 48；目录关闭时沿用 16 读 + 13 写 = 29。`MCP_WRITE_ENABLED=false` 不注册任何写工具。`KUBERNETES_WRITE_ENABLED=false` 阻止集群变更，但仍允许目录配置、模板和台账的数据库写入；两个开关不能混为一谈。REST 数据库操作沿用 REST key，无新写开关。MCP 标注不替代未来权限。

### 保存新 Redis 配置示例

```json
{
  "ownerId": "alice",
  "name": "redis-demo",
  "namespace": "demo",
  "software": "redis",
  "templateVersion": 1,
  "parameters": {"storageClass": "standard", "secretName": "alice-redis"},
  "expectedRevision": 0,
  "requestId": "alice-redis-config-001"
}
```

HTTP：`POST /api/v1/services/save-config`；MCP：`save_service_config`。响应包含 `instance` 与 `revision`，其中修订含完整 parameters/YAML/digest。更新时补 instanceId，保留身份字段，expectedRevision 使用当前修订，requestId 换新。

### 准备与执行

```json
{"instanceId":"<上一步ID>","revision":1,"port":30032,"requestId":"alice-redis-deploy-001"}
```

HTTP：`POST /api/v1/services/prepare-deployment`；MCP：`prepare_service_deployment`。端口只是调用示例，必须以实时台账为准，不保证 30032 空闲。

```json
{"deploymentId":"<prepare返回ID>"}
```

同一输入分别用于 deploy、deployment、verify-access。实例删除输入为 `{"instanceId":"...","requestId":"delete-001"}`，固定保留 PVC。模板 get 输入 `{software,version}`；模板 save 输入 `{software,version,defaults,manifestTemplate?}`。manifestTemplate 留空使用标准骨架；支持 `.Name/.Namespace/.Software/.InstanceID/.BackendPort/.Parameters` 与 `q` JSON 字符串转义函数，默认值和参数均为已定义 Parameters 字段。

手工预留输入 `{instanceId,port,requestId,note?}`，必须关联已有实例；每个实例同时只持有一个外部端口，标准 prepare 能消费该实例同端口的预留。释放输入 `{port,requestId}`。port list/events 可按 `{port?,state?,limit?,offset?}` 查询。手工释放不跳过实际路由和当前部署检查。

## 状态与失败恢复

台账：`unchecked → free → reserved → occupied → releasing → free`。`external` 是外部已用入口，不能自动接管；`conflict` 字段独立标识外部路由冲突。未完整读取 Service、Gateway 和全 Namespace TCPRoute 时，空闲项回到 unchecked；已预留/已占用记录保留，分配拒绝。端口实际是否暴露用 `exposed` 表示，free 并不保证能访问。

首次完整同步导入已有映射，不预写 11 个快照端口。只要外部路由声明引用某端口，即使对应 Listener 尚未建立，也阻止分配；没有 port/sectionName 的通配 parentRef 会保护所有可能匹配的端口。外部 Listener 和孤立 Service 暴露项保守标记 external。已有 external 不因一次消失就自动变 free，须显式核对释放。routeMappings 保留实际 Namespace/Route/后端 Service/端口；台账变化、领用、绑定和释放事件保存当时的 portSnapshot，便于查看原占用者与映射。

部署：`prepared → applying → applied/ready`，错误或进程中断进入 `unknown`，端口保留。`stage` 标记 dry-run/gateway/resources/observation 等步骤，`attempts` 记录执行次数，资源清单保存 UID。重试同一 deploymentId 先核对归属/UID；已完成部署重复调用只读状态。相同实例的部署/删除串行，全局入口修改与同步串行；共享对象使用最新 resourceVersion 和冲突重试。

`phase` 是最后保存的执行状态，`observation` 是最近查询的即时状态。get 工具不修改数据库，因此 phase 为 applied 而 observation 全部就绪是正常的；不能只看 apply 成功判断可用。verify-access 是独立只读报告，包含 checkedAt，未持久化；部署历史保存的是提交时的观察。

删除先移除受管 TCPRoute，再清理 StatefulSet、Service、ConfigMap，以记录的 UID 作删除前置条件。若资源换了 UID，不自动删除替代物。等待非 PVC 资源真正消失并完成入口回读，才回收端口；finalizer、超时和残留路由导致 DELETE_PENDING，重试同一删除请求。保留 PVC 和共享入口暴露端口/Listener。没有执行过的 prepared 部署可通过 release 取消，不能藉此释放已有资源或未知结果部署。

业务失败示例：PORT_IN_USE、PORT_NOT_IN_POOL、GATEWAY_SYNC_INCOMPLETE、REVISION_CONFLICT、MISSING_PARAMETER、IDEMPOTENCY_CONFLICT、RESOURCE_IDENTITY_CONFLICT、DEPLOYMENT_RESULT_UNKNOWN、DELETE_PENDING。REST HTTP 400 使用统一 code/msg/data，`data.reason` 是业务码；未分类依赖错误为 HTTP 500 通用结构。MCP 返回 isError 与同一业务码，不套 REST envelope。

## 权限预留与实际限制

目录所有公开用例在副作用前调用 `catalog.Service.policy`，默认 DeferredAuthorizer 放行；部署适配器还复用现有 cluster Authorizer。当前 ownerId **只是元数据，不是已认证身份**。未来用可信 context Principal 绑定 owner/instance，再按 catalog.template.save/config.save/port.reserve/deployment.execute/instance.delete 等动作授权，不信任工具输入的 ownerId。未实现认证、角色、审批、自动脱敏或新权限策略。

目录读数和 YAML 按原文返回。现有 get_resource 等历史过滤行为不在本轮改动范围。凭据引用以外的配置也可能敏感，数据库备份应按配置资产保管。范围检查和写开关继续有效。Namespace allowlist 必须同时包含实例 Namespace 与 Higress Namespace；台账同步需要固定集群上**全 Namespace 列出 TCPRoute**，用于发现共享入口占用；Secret 仅读取已保存实例明确引用的一个对象，不返回 Secret 内容。

初版没有 HA、多集群注册、自动补 Namespace/Secret、备份调度、数据库大版本升级、PVC 删除、自动超时回收或后台 rollout watch。若真实资源被其他控制器或人替换，需要人工核对 UID/台账，不靠重试强行接管。

## 验证范围

- SQLite 持久化、重复迁移、事务回滚、两个独立连接并发抢同一端口。
- Redis/PG 渲染、缺参、不可变版本、乐观锁、幂等、禁止缩盘和数据库镜像变更。
- 模拟部分 apply、中断重启、结果未知后重试、未就绪不宣称 ready。
- 共享 Service/Listener 增量更新、resourceVersion 冲突重试、全 Namespace route 导入、孤立 route 占用。
- 按 UID 删除的实际 HTTP 请求、保留 PVC、删除挂起不释放、重复部署不重复 apply。
- 真实 MCP SDK 与 HTTP 共用记录、工具发现数量/读写标注、业务错误码和 REST key/request ID。

执行 gofmt、go test ./...、go vet ./...、静态 CGO_ENABLED=0 构建。容器完整构建目前遇到 Docker Hub 拉取基础镜像超时/连接被拒绝，须网络恢复后补验。真实 Redis/PG、LB 转发、StorageClass 和协议访问需要部署环境联调，本次没有向用户集群写入。
