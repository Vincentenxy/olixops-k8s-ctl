# 标准服务配置、独立实例与端口管理开发计划

2026-10-26。以下保留原始八阶段需求，交付状态见本节；详细实际行为以 [标准服务实现文档](standard-services.md) 为准。

## 本轮交付状态

八阶段的存储、标准模板、实例/配置、台账、准备、部署、删除及 HTTP/MCP 已实现，并增加显式只读访问验证。模拟集群与真实 SDK 测试覆盖持久化、并发、幂等、中断、共享入口增量更新和删除保护。代码权限策略仍只预留，未新增认证、角色或自动脱敏。

尚未进行真实 Redis/PG/LB 部署验收；首次真实部署的 server dry-run、存储、Admission、转发和协议连接须在部署环境核对。Docker 构建遇到基础镜像拉取超时。本文末尾的“完成条件”是验收标准，并不表示真实环境已验证。

## 目标与范围

- 本轮只部署 olixops-k8s-ctl，一个进程内提供普通 HTTP 与 MCP 接口。
- SQLite 嵌入进程，数据库保存到持久目录，不另部署数据库服务。
- 每位用户分别拥有独立 Redis、PostgreSQL 实例；实例分别管理工作负载、Service、配置、凭据引用和 PVC。
- 所有实例共用 Higress LB，通过不同外部 TCP 端口访问。
- 端口池为 30000–30200，包含两端共 201 个端口。每人各一个 Redis 和 PostgreSQL，需要两个端口。
- 本地智能体理解需求并调用工具，服务端按固定标准模板生成 YAML。
- 权限仅预留接入点，本轮不新增身份系统、角色、审批或自动脱敏策略。
- olixops-agent 本轮无需部署或修改，后续可以复用这些接口。

2026-10-26 只读核对：当前集群 higress-system/higress-gateway 是 LoadBalancer，地址为 10.77.75.40；业务入口为 higress-system/higress-tcp-gateway。存在 11 个业务 TCP 端口及对应 TCPRoute：30000、30001、30030、30050–30055、30060、30100。上述仅是当日快照，实施时必须重新读取。没有验证从用户网络访问这些地址。

## 1. 存储与数据契约

加入 SQLite 驱动、数据库初始化、版本迁移、事务和关闭流程，兼容现有 CGO_ENABLED=0 构建方式。数据库默认路径建议为 /data/olixops.db，通过配置覆盖。容器先采用单副本，数据库目录挂载持久卷，确保非 root 运行用户可写。

建议的最小业务表：

| 表 | 职责 |
| --- | --- |
| service_templates | 标准模板的不可变版本、软件类型、schema、默认参数和 YAML 模板 |
| service_instances | 稳定实例身份、ownerId、名称、Namespace、当前配置与部署指针 |
| service_revisions | 每次保存的参数、固定模板版本、生成 YAML、摘要和版本号 |
| service_deployments | 选定配置修订、端口领用、部署阶段、执行尝试、资源身份和验证结果 |
| gateway_ports | 入口、协议、外部端口、暴露信息、占用者、实例与部署关联 |
| port_events | 导入、预留、绑定、释放、冲突和人工核对历史 |

入口配置先使用 Viper，明确当前集群标识、Higress Service 和 Gateway。HTTP 与 MCP 接收同一套业务输入。

**完成条件：** 重启后记录仍在；迁移可重复执行；事务回滚不留半条领用记录；数据库文件不进入 Git。

## 2. Redis / PostgreSQL 标准模板

提供单实例 Redis 与 PostgreSQL 模板，初始化导入 SQLite。运行时查询和部署使用数据库中的确定版本；初始化不能覆盖已有模板版本。

模板定义镜像、CPU/内存、持久化容量、StorageClass、应用配置、Service、探针、凭据引用及可覆盖参数。第一版使用 StatefulSet、Service、PVC 和 ConfigMap 等现有允许的资源。凭据通过已有 Secret 引用，现有通用 apply 不支持创建 Secret，不把真实凭据写入 Git。

模板固定与应用版本匹配的数据目录、启动参数和配置挂载方式。分别保存外部端口与后端端口，不能把用户领用端口直接作为 Redis/PostgreSQL 进程监听端口。Redis 默认后端端口 6379，PostgreSQL 默认后端端口 5432。

配置中有明确默认值才可自动采用；Namespace、StorageClass 或凭据引用缺失时，返回缺参结果供智能体追问。第一版不自动升级数据库主版本或缩小 PVC。

**完成条件：** 参数经过 schema 校验；渲染结果符合资源白名单并通过 server dry-run；模板修改产生新版本，旧配置仍可重现。

## 3. 用户实例与配置保存

实现实例创建、配置保存、预览、列表、详情与修订历史。一个用户可以拥有多个实例，每个实例有独立 ID 和资源名称；不依赖名字中包含用户名判断归属。

ownerId 本轮作为归属元数据保存，暂不把调用者传入的值当作已经验证的身份。预留从可信请求上下文解析身份和授权的位置。

每次保存新增不可变修订，记录完整参数、模板版本、YAML 和摘要。使用 expectedRevision 防止旧配置覆盖新配置，使用 requestId 和请求内容摘要处理重复及冲突请求。配置保存不直接执行 Kubernetes 操作，也不占用端口。

**完成条件：** 两位用户的实例能准确区分；资源名称不冲突；历史修订内容固定；重复保存不产生额外修订。

## 4. 端口池、已有占用与领用历史

初始化 30000–30200 的 201 条端口记录。读取 Higress Service、Gateway Listener 和所有相关 TCPRoute，导入当前映射；已存在的 11 个映射先标记为外部已有占用，不自动接管。

为每条记录保存：入口 ID、协议、外部端口、ownerId、实例 ID、部署 ID、Namespace、Service、后端端口、Listener、TCPRoute、领用/释放时间和备注。

端口唯一性按入口、协议、外部端口约束，不能按用户或 Namespace 分割同一 LB 的占用空间。台账状态与实际入口状态分别保存：候选端口未暴露，不等于端口已可访问。

领用在 SQLite 事务中完成，带幂等标识；状态包括待核对、空闲、预留、已占用和待释放。导入无法完整读取时标记待核对，不能假定空闲。外部手工改动通过回读发现并记录冲突。

**完成条件：** 两个并发请求抢同一端口只有一个成功；现有端口不会重新分配；20030 返回范围错误；30030 按当前实际占用返回冲突。

## 5. 准备标准部署

提供 prepare_service_deployment：接收实例 ID、固定配置修订、入口、外部端口和 requestId，检查范围及现有占用，原子预留端口并保存部署记录。

以选定配置修订生成最终部署产物，固定模板、实际参数、端口映射、YAML 和摘要，返回 deploymentId。后续部署按该 ID 读取；不让智能体重新拼接或修改 YAML。

相同请求返回原部署记录；相同 requestId 携带不同参数时返回冲突。准备操作属于写入，须在 MCP 中正确标注。

**完成条件：** 智能体重复提交不会创建第二个实例或领取第二个端口；模板更新不会改变已经准备的部署产物。

## 6. Higress 接入与标准部署执行

提供 deploy_service(deploymentId)，在同一业务层复用现有校验、dry-run 和 apply。内部直接调用业务方法，不绕 HTTP/MCP 调用自己。

入口适配负责增量维护 higress-gateway Service 端口与 higress-tcp-gateway Listener，保留全部已有配置并处理 resourceVersion 冲突。业务 Namespace 内应用实例资源和 TCPRoute。现有 apply 要求单 Namespace，不能将 higress-system 的共享对象与实例资源拼入一次通用 apply。

为部署记录保存受管资源名称、UID、修订关联与执行阶段。开始执行前持久记录状态；超时、中断或进程重启后先核对实际资源，不盲目重复执行或释放端口。相同实例的部署和删除串行处理。

依次记录资源已提交、Pod/PVC 状态、Gateway/TCPRoute 条件和访问验证结果。TCP 连接成功与 Redis PING/读写、PostgreSQL 实际查询分开记录；从服务端可达不能代替用户网络可达。

**完成条件：** 新实例通过所领用外部端口访问；原有路由保持不变；部分成功和结果不明可核对；apply 成功不被报告为服务已可用。

## 7. 实例删除与端口回收

提供显式删除操作，按实例资源清单和 UID 清理受管工作负载、Service 和 TCPRoute；PVC 默认保留，并显示保留结果。不删除共用 Higress LB 或 Gateway，不清理未归属于该实例的资源。

先标记待释放，确认实例路由解除后再将端口恢复为空闲，保留领用历史。保留共享 Listener 和 Service 暴露端口也可完成回收，不能将清空共享入口作为释放前提。

部署失败、健康检查失败、智能体断线和超时均不能直接触发端口释放。准备后尚未执行的取消可以在确认没有实际资源绑定后回收。第一版不做无人核对的超时自动释放。

**完成条件：** 删除不影响其他用户；保留数据卷；失败时不产生实际占用与台账空闲的矛盾。

## 8. 普通接口、MCP 与交付验收

按功能同步交付普通接口和 MCP，二者共用 application 用例，不各自实现状态机。普通接口使用 /api/v1/资源，业务仅使用 GET/POST，沿用统一响应；MCP 沿用 JSON-RPC。

| HTTP 分组 | 主要行为 | 建议 MCP 工具 |
| --- | --- | --- |
| /api/v1/templates | 列表、指定版本详情、保存新版本 | list_service_templates、get_service_template、save_service_template |
| /api/v1/services | 实例创建/配置保存、预览、列表、详情、修订历史 | save_service_config、preview_service_config、list_service_instances、get_service_instance、list_service_revisions |
| /api/v1/services | 准备、执行、部署状态/历史、显式删除 | prepare_service_deployment、deploy_service、get_service_deployment、list_service_deployments、delete_service_instance |
| /api/v1/ports | 查询、同步台账、手工预留、核对后释放、领用历史 | list_gateway_ports、sync_gateway_ports、reserve_gateway_port、release_gateway_port、list_gateway_port_events |

同步台账虽然不改集群，仍属于数据库写入。手工预留和标准部署复用同一领用逻辑；释放接口不能绕过路由核对。提供 PORT_IN_USE、PORT_NOT_IN_POOL、REVISION_CONFLICT、MISSING_PARAMETER 等可识别业务结果，方便智能体追问或调整。

按既定要求仅预留权限校验，不新增权限策略或自动脱敏；现有范围约束和写开关继续适用。已有 get_resource 等工具的历史行为不属于本轮改动范围。

更新 README、MCP 文档、运行配置及镜像。镜像当前没有安装 kubectl/Helm，需要补齐实际使用的执行依赖；提供 SQLite 持久卷示例，按单副本运行。

**完成条件：** HTTP/MCP 结果一致；配置持久化、修订、并发抢端口、重复调用、进程重启、部分失败、既有占用、共享 Gateway 增量修改和删除回收均有行为验证；运行 gofmt、go test ./...、go vet ./...。真实部署单独记录目标、结果和清理范围。

## 最小验收场景

用户 A 创建独立 Redis 并领取一个空闲端口，创建独立 PostgreSQL 并领取另一个端口。用户 B 同样创建独立实例，使用不同端口与数据卷。两位用户的实例共用 LB，服务名、配置、凭据引用和存储分别关联。

本地智能体只需发现标准模板、补齐必要参数、保存配置、准备部署、调用部署并查询结果。出现端口冲突时不覆盖既有资源，应返回占用信息并让用户选另一个端口。

上述能力现已实现。开发测试使用临时 SQLite 与模拟集群，没有创建真实数据库实例或修改集群端口。
