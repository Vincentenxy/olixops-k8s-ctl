# publish / k8s-ctl 服务生命周期覆盖分析

分析日期：2026-11-14。依据：正在运行的 IDEA publish 项目、重新发现的 54 个 publish MCP 工具、对应实现和项目手册，以及此前的真实链路测试。本轮仅查询工具目录和阅读代码，没有执行部署、卸载、生产变更或新增业务接口。

## 结论

对于**已经在平台注册、已有镜像的服务**，publish 与 k8s-ctl 已基本覆盖非生产环境的配置、安装、升级、运行查询、排障、修复和卸载。可以组织成开发人员日常自助使用的流程。

如果“完整生命周期”包括创建服务、源码构建、制品发布、跨环境上线、生产审批执行、可靠回退、长期可观测以及永久退役，**当前 publish MCP 尚未完整覆盖**。需要区分：已有 MCP、后端已有但未开放 MCP、由外围系统承担、仍需设计的业务流程。不能把项目没有 MCP 入口解释为项目后端没有实现。

## 逐阶段对照

| 阶段 | 当前 publish MCP / 可复用能力 | 覆盖判断与边界 |
| --- | --- | --- |
| 服务登记与归属 | `search-publish-services` 查询目录；后端已有服务 create/update/delete 和流水线关联接口 | 部分。已有服务能定位；新建服务、绑定环境/集群及流水线未发现对应 MCP |
| 本地开发与运行参数 | 配置 context/query/references、分组查询、Chart 查询；项目另有本地 SDK 接入手册 | 部分。可查询运行所需配置；源码编写、本地编译测试由开发者或本地 Agent 承担，54 工具中没有统一的本地开发执行入口 |
| 依赖与变量准备 | 查询 EnvVault 分组和引用；创建数据库/网络发布单；服务配置可引用已有依赖 | 部分。查询引用不等于创建变量/账号；创建发布单不等于执行 SQL/开通网络。publish 的 8 个下层代理未接入标准 Redis/PG provisioning，当前操作器禁用 Catalog |
| 构建与制品 | 后端有流水线关联；当前 MCP 可查询已部署镜像和保存配置中的镜像信息 | 未形成 MCP 构建链。未发现触发 CI、查询构建结果/日志、确认制品 digest 和构建质量的工具；不据此断言外围 CI 不存在 |
| 配置初始化与修改 | `init-service-config`、`query-service-config-context`、`query-service-config`、`save-service-config`、配置历史/引用/Chart 工具 | 已有主要能力。init 仅 magic-v2 且只补缺失文件；保存不是校验，不创建 EnvVault 变量，也不部署 |
| 部署预检 | `check-service-config-render`、`validate-service-config`、`preview-service-deploy`、精确版本 Chart context/schema | 部分。变量/YAML 与 Chart 定义检查已有；不提供完整 Helm 模板渲染或集群准入预检。此前 Chart 2.2.8 预览曾有默认文件检查问题，见验收报告 |
| 非生产首次安装与更新 | `query-namespace`、`create-namespace`、`deploy-service(install/upgrade)`、`query-service-deploy-status` | 主要链路已有，并有真实安装/卸载验收。不能只依据部署接口成功判断应用已就绪；需回读 Pod/副本/镜像/事件 |
| 测试与上线准备 | Jira/人员查询、SQL 规则查询、四类发布单创建与校验、批次回读、发布计划查询和拓扑编辑 | 部分。能准备发布材料、查看状态及依赖，不能把创建发布单或保存拓扑算成执行发布。业务自动化测试、验收录入、审批流执行未发现对应 MCP |
| 生产发布 | 既有平台发布与审批 HTTP/业务实现可复用；MCP 有环境守卫 | 未覆盖完整生产 MCP 流程。默认 `allow-prod-environment=false`，相关生产读写均可被拦截；本轮未核验部署配置是否显式放开。启用开关也不代表审批、执行、回退和验收流程齐备 |
| 运行维护 | `scale-service`、状态/事件查询、8 个 `k8sctl-*` 日志/Shell/终端/文件工具 | 主要人工操作已有。服务重启、Pod 删除有现成 HTTP 业务，但未发现对应 publish MCP；scale 0→1 是停启，不能等同于滚动重启；运行期 scale 不保存配置中的副本数 |
| 应用问题排查与记录 | diagnostics/status、logs、配置源/历史、Chart 精确版本、知识搜索/读取、运维文档查询/追加、Shell/文件 | 常见问题可排查并记录。已有配置错误案例及真实容器操作证据；没有发现集中历史日志、指标时间序列、告警、调用链/APM 等 MCP，不能承诺任意问题均可自动定位 |
| 修复与历史回退 | 配置保存→预检→upgrade→状态/访问验证；`query-service-release-history` 查询 magic-v2 归档 | 配置修复流程可组合。历史查询不执行回退；magic-v2 快速回退 HTTP 已实现，但未发现 MCP 入口。普通 Helm revision、源配置 revision、发布归档版本与镜像版本不能互相替代 |
| 部署后验收 | `query-service-deploy-status`、`verify-service-access`、`probe-network-links` | 部分。Pod Ready 与后端视角 HTTP/网络连通可验证；不等于用户网络可达、登录流程/业务接口完整通过，也不是持续监控 |
| 停服与卸载 | `scale-service(replica=0)`、`deploy-service(uninstall)` | 已有，并有真实测试。卸载 release 与永久注销服务不同；不能认为所有 PVC、Namespace、DNS、网络规则、变量和平台记录都已删除 |
| 永久退役与资源回收 | 后端有服务目录删除；当前 MCP 提供运行卸载 | 部分。缺少面向 Agent 的完整退役检查/执行/验收流程，需明确依赖、共享资源、数据保留、账号和网络回收、归档及目录状态 |

“已有”指源码和工具契约具备能力；不表示所有部署模式、环境和 Chart 已完成真实验收。前轮服务 `2963078505` 为 magic；magic-v2 的历史归档/快速回退不能套用到该服务。

## 当前就能组织的使用流程

前提：服务定义与目标环境已存在，镜像已构建推送，必须的变量和外部依赖已就绪，目标环境与操作已得到授权。

1. 用服务目录/context 工具确定唯一 serviceId、环境、模式和部署位置。
2. 查询当前配置、变量引用和精确版本 Chart，准备配置；仅在 magic-v2 文件确实缺失时 init。
3. 保存并回读配置，分别执行变量/YAML 检查与 Chart 定义预览；保留未检查项，不用解析成功替代部署验收。
4. 查询 Namespace，授权范围内缺失时创建；执行 install/upgrade 一次。
5. 回读运行状态，确认实际镜像、期望/就绪副本和事件；按能力验证入口与业务测试。
6. 异常时定位证据，修改已确认有问题的源配置，再保存、检查、升级并回读。
7. 向服务运维文档追加现象、原因、处理和验证结果。
8. 需要暂时停服则 scale=0；需要卸载则 uninstall，明确仍保留的资产；不将卸载冒充永久退役。

serviceId + env 应贯穿整个流程。选择后不随步骤回退默认 dev；多个 Pod/容器需要选择时，使用平台返回的候选。请求超时后先回读状态，不自动重放写操作。

## 排障工具的使用优先级

按问题相关性组合现有工具，不要求每次排障遍历全部工具：

1. **已有知识与服务运维记录**：搜索相同异常关键字，作为线索，不将历史原因当本次结论。
2. **实际状态、事件、日志**：确认服务/环境和异常 Pod；CrashLoop 场景优先确认 previous 日志。
3. **部署源与精确 Chart 版本**：读取 values/envs 或 values-desc/envs-desc，以及该版本字段定义；核对变量解析、挂载路径、多行配置写法和版本差异。
4. **现场差异核实**：必要时查看状态返回的资源 YAML，或通过授权 Shell/文件传输核对实际配置和进程信息。源定义能解释的问题先用源定义验证，不无条件枚举 ConfigMap。
5. **修复与验收**：源配置修改经检查后部署；回读 Ready、日志和业务行为，向运维文档追加结果。

现有服务源、变量和 Chart 版本应作为可追溯依据。只有排除配置及版本适配问题后，才提出 Chart 模板可能存在问题，并联系运维维护者核查。

## 优先补齐什么

| 优先级 | 建议 | 复用位置 / 实施理由 |
| --- | --- | --- |
| 第一批 | 服务滚动重启 MCP | 已有 `POST /api/v2/service/status/restart` 与 `IAppStatusService.restart`；先核对策略语义，再以 serviceId/env 暴露。避免再次实现底层命令 |
| 第一批 | 按历史快照回退 MCP | 复用 `POST /api/v2/deploy/quickRollback` / `ServiceQuickRollbackService`。现实现仅 magic-v2 的成功归档，恢复配置及服务自身变量并重新部署，跳过共享变量；MCP 应保留这些边界 |
| 第一批 | 串联生命周期的 Skill/工作流和验收报告 | 使用已有配置、部署、状态、排障、记录能力，明确阶段结果与失败恢复；已有后端异步/历史实现应复用。当前 MCP 还没有统一任务进度与恢复契约 |
| 第二批 | 服务登记与 CI 接入 | 服务创建/更新、流水线关联复用已有 HTTP 服务；触发构建、结果/日志查询接入现有 CI；关联 commit、image digest、配置 revision 和发布归档 |
| 第二批 | 变量/依赖的跨模块工作流 | 与 EnvVault、数据库、网络既有系统组合。查询变量、创建发布单与真正完成资源供应分别验收，不在 publish 重做各系统 |
| 第二批 | 下线预检与最终退役 | 先列明依赖、资源归属和保留策略，再组合现有卸载/平台注销能力；执行后验证残留，保留档案。共享 Namespace/PVC/变量不按服务名盲删 |
| 后续生产阶段 | 审批、执行、验收和回退接入 | 复用已有平台流程，补 MCP 缺口及真实环境验收；恢复/核实服务级授权和操作审计，不能只修改生产开关 |
| 后续观测阶段 | 指标、历史日志、告警及 APM | 优先对接已有观测平台；下层 Shell 保留按需现场诊断职责 |

不建议为了生命周期覆盖，把下层 57 个工具原样代理到 publish。上层应继续面向服务和业务环境，复用现有安装、发布、配置与权限逻辑；下层负责集群执行。尤其“按服务历史回退”涉及平台记录、Git 源和变量快照，应复用 publish 的业务回退，不用单独 Helm 回滚代替该业务语义。

本轮不实施上述建议，不改生产开关，不恢复或重写权限规则。是否提供 MCP 入口与是否放开生产操作属于不同决策。

## 主要证据

- 当前 MCP `tools/list`：54 个，8 个 `k8sctl-*`，未发现 restart、rollback、构建触发、服务登记/注销、审批执行类工具；原始 Schema 存在本机 Git 忽略目录。
- [首次部署手册](/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api/docs/manuals/SERVICE_IMAGE_DEPLOY_GUIDE.md:3)：明确前提是已有服务定义和镜像。
- [服务部署 MCP](/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api/src/main/java/net/gtjaqh/pubdevops/mcp/ServiceDeployTools.java:73)：install/upgrade/uninstall 与环境守卫。
- [生产环境开关默认值](/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api/src/main/java/net/gtjaqh/pubdevops/mcp/config/McpSecurityProperties.java:18)、[环境守卫](/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api/src/main/java/net/gtjaqh/pubdevops/mcp/config/McpEnvironmentGuard.java:23)。
- [服务登记/注销与流水线关联](/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api/src/main/java/net/gtjaqh/pubdevops/controller/MicroserviceBasicController.java:31)。
- [已有服务重启 HTTP](/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api/src/main/java/net/gtjaqh/pubdevops/controller/v2/ServiceStatusV2Controller.java:93)。
- [已有快速回退 HTTP](/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api/src/main/java/net/gtjaqh/pubdevops/controller/v2/ServiceQuickRollbackController.java:27)、[模式和历史校验](/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api/src/main/java/net/gtjaqh/pubdevops/service/v2/deploy/impl/ServiceQuickRollbackService.java:276)。
- [后端已有异步发布任务实现](/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api/src/main/java/net/gtjaqh/pubdevops/service/v2/deploy/impl/ServiceReleaseTaskService.java:40)：不将 MCP 缺少任务契约解释为平台没有任务实现。
- [本轮 57 工具双入口验收矩阵](testing/k8sctl-http-mcp-tool-matrix-2026-11-14.md)、[publish 生命周期实测](testing/publish-service-lifecycle-2963078505-2026-11-14.md)、[真实问题排查案例](troubleshooting/case-8824195328-sim-nginx-chart-version.md)。
