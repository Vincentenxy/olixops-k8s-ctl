# publish MCP 服务生命周期真实验收清单

日期：2026-11-14，Asia/Shanghai。执行窗口：14:39:53–14:45:57。目标：服务 `2963078505` 的仿真环境 `sim`。

## 1. 结论

通过 publish 平台 MCP 完成了 **停启式重启、卸载、原版本重新安装，以及中间状态和最终状态回读**。本轮 15 个验收检查点通过；另有 1 项预览检查未通过、1 项独立滚动重启能力未提供，分别记录，不能合称所有检查均通过。

结束时服务已恢复安装并达到 **1/1 Ready**。源配置、Deployment spec 和实际镜像 digest 与基线一致。Deployment 和 Pod 身份已更新，最终容器 restartCount=0。

本次只补未完成的生命周期测试。按用户要求，没有重复日志、Shell、持续终端、文件上传下载的既有正常流程，也没有测试 Higress 入口端口台账。

## 2. 目标与执行方式

| 项目 | 实际值 |
| --- | --- |
| 服务 ID / 名称 | `2963078505` / `setting-efficient-api` |
| 环境 | `sim` |
| 部署模式 | `magic`，使用 `values`、`envs` |
| 集群分组 / 实际集群 | `lucy-mid-k8s` / `gdl-dev-lucy` |
| Namespace | `setting-efficient` |
| Deployment / 容器 | `setting-efficient-api` |
| 原版本及恢复版本 | `lucy-chart-release/magic-app:2.2.8` |
| 镜像 | `harbor.gtjaqh.net/infra/nginx:mainline`，未改镜像或拉取策略 |
| 原副本 / 最终副本 | `1` / `1` |
| 源配置 revision | `4512a44dac2f4bab0a164632cd3c2989dfe1939d` |
| publish | IDEA 项目 `/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api` |
| 操作入口 | 已配置的 publish `/pubops/mcp`，复用现有认证 |

用户已明确授权这个服务、环境的安装、卸载和重启。所有业务操作均为 publish MCP `tools/call`；本地脚本只负责 MCP 协议、私有证据保存与结构化断言。没有直接调用下层 HTTP、kubectl、Helm CLI 或 Kubernetes API 执行集群操作。

先读取并遵循 `service-deploy`、`service-status-query`，以及 publish 知识库中的“常规部署与升级”说明。实际资源静态目录尚未登记集群用途，本次沿用平台实时解析的服务位置，不从文档示例推断部署目标。

## 3. 通过清单：15 个检查点

以下均以真实 MCP 响应和回读结果判断，不以工具存在、HTTP 成功或模拟测试替代。

| 编号 | 检查点 | MCP 工具 | 实际证据 | 结果 |
| --- | --- | --- | --- | --- |
| T01 | 读取运行基线 | `query-service-deploy-status` | 原服务 1/1 Ready；原 Pod `setting-efficient-api-6cdb858d56-6nrf8`，restartCount=0 | 通过 |
| T02 | 原配置能按平台逻辑转换 | `query-service-config`、`check-service-config-render` | 两份源文件读取成功，masked=false；转换报告 valid=true，覆盖 environment-structure、references、rendered-yaml | 通过 |
| T03 | 提交停止请求 | `scale-service`，replica=0 | isError=false、code=0；实际 spec.replicas 变为 0 | 通过 |
| T04 | 观察停止过程 | `query-service-deploy-status` | 原 Pod 进入 deletionTimestamp 非空的终止过程，没有将仍显示 Running/Ready 的旧 Pod 认作可用 | 通过 |
| T05 | 确认停止完成 | `query-service-deploy-status` | 期望副本 0，平台回读该 Deployment 的 Pod 列表为空 | 通过 |
| T06 | 提交恢复请求 | `scale-service`，replica=1 | isError=false、code=0；实际 spec.replicas 恢复为 1 | 通过 |
| T07 | 观察重启恢复过程 | `query-service-deploy-status`、`query-service-diagnostics` | 恢复请求成功后曾为 0/1，Available=False；没有把提交成功直接记为恢复成功 | 通过 |
| T08 | 确认停启式重启完成 | `query-service-deploy-status` | 新 Pod `setting-efficient-api-6cdb858d56-mpz9x`，UID 不同，最终 1/1 Ready；Deployment UID 保持原值 | 通过 |
| T09 | 卸载已安装服务 | `deploy-service`，operate=uninstall | isError=false、code=0，返回同一服务和 sim 环境 | 通过 |
| T10 | 卸载后回读部署视图 | `query-service-deploy-status` | Deployment 列表、Ingress 列表为空；平台记录的 Chart 名称/版本为空 | 通过 |
| T11 | 卸载保留 Namespace | `query-namespace` | `setting-efficient` 的 exists=true | 通过 |
| T12 | 卸载保留配置源 | `query-service-config` | revision、values 与 envs 的 SHA-256 均与基线一致 | 通过 |
| T13 | 原版本重新安装 | `deploy-service`，operate=install | 显式传入原 Chart 及 2.2.8，isError=false、code=0；未使用平台推荐版本替换 | 通过 |
| T14 | 观察安装过程并等待就绪 | `query-service-deploy-status`、`query-service-diagnostics` | 捕获新 Pod Pending / ContainerCreating、0/1；后续收敛为新 Deployment、新 Pod、1/1 Ready | 通过 |
| T15 | 核对最终恢复一致性 | `query-service-deploy-status`、`query-service-config`、`query-service-config-context` | Chart 确认 2.2.8；Deployment spec、配置 revision/哈希、镜像 digest 均未变化；最终 restartCount=0 | 通过 |

T10 只证明平台可见的 Deployment/Ingress 视图为空，以及卸载调用成功，不扩大为对所有 Kind 的独立逐项删除验收。未额外检查实时 Service、ConfigMap、ReplicaSet 或任何 PVC/PV；本次没有执行持久卷删除操作。

T15 的 spec 一致性通过解析前后 Deployment YAML 后比较 `spec` 对象确认，排除 metadata/status 的正常身份和时间变化。实际镜像 digest 通过前后 Pod 的 `status.containerStatuses[].imageID` 比较确认；不是仅比较可变镜像 tag。

## 4. 执行顺序与关键时刻

| 本地时刻 | 操作或观测 | 结果 |
| --- | --- | --- |
| 14:39:53 | 读取运行基线 | 1/1 Ready，Chart 2.2.8 |
| 14:40:38 | 请求副本 1→0 | MCP 约 0.709 秒返回成功 |
| 14:40:39 | 回读停止过程 | spec=0，原 Pod 仍在终止 |
| 14:41:39 | 回读停止完成 | 无 Pod |
| 14:41:47 | 请求副本 0→1 | MCP 约 1.093 秒返回成功 |
| 14:41:48 | 回读恢复过程 | spec=1，尚未就绪 |
| 14:42:50 | 回读重启完成 | 新 Pod 1/1 Ready |
| 14:43:10 | 请求卸载 | MCP 约 1.541 秒返回成功 |
| 14:43:19 | 回读卸载后状态 | 无 Deployment、Ingress；Chart 记录为空 |
| 14:43:37 | 查询 Namespace | 仍存在 |
| 14:43:48 | 请求原版本安装 | MCP 约 3.634 秒返回成功 |
| 14:43:57 | 回读安装启动阶段 | 新 Pod Pending / ContainerCreating，0/1 |
| 14:44:56 | 观察到安装收敛 | 新 Pod 1/1 Ready |
| 14:45:57 | 最终复查 | 仍为 1/1 Ready，restartCount=0 |

时刻为本地调用开始时间，Ready 时间是采样观测界限，不是精确就绪时间；对话编排与轮询间隔包含在观测跨度中，不将它当作集群性能基准。单个部署/扩缩容请求分别只提交一次；等待期间只做只读查询。

## 5. 本轮未通过及能力限制

### X01：部署预览的字段预检未完成

调用 `preview-service-deploy`，明确指定原版本 2.2.8，使用读取到的完整配置。协议与平台调用成功，但报告 `passed=false`，不能记为通过：

```text
stage: chart-schema
message: 精确Chart的默认配置或字段说明不可用，未完成字段预检
warning: Chart包未提供根目录values.yaml
```

同一版本查询已返回 schemaAvailable=true、defaultsAvailable=false；源配置转换 valid=true。此处暴露的是预览工具对根目录默认配置缺失的处理限制，本次没有修改源配置、Chart 或预览代码。已经确认的当前运行基线与原版本支持本次恢复测试，实际安装及 Ready 验收另行完成，不能反过来把字段预检说成通过。

后续可评估在字段说明可用、没有根目录默认文件的情况下，如何准确表达“无默认值”与“无法校验”，并完善预检行为；这属于平台工具改进，不以调整 Chart 制品作为本次建议。

### X02：平台没有独立滚动重启工具

本次实际 `tools/list` 返回 54 个 publish 工具，没有独立 restart/rollout-restart 工具。`deploy-service` 仅支持 install、upgrade、uninstall。

因此本轮使用已有 `scale-service` 先停到 0，确认旧 Pod 消失，再恢复到原副本 1，实现并验收了**停启式重启**。这会产生服务中断，不能将其描述为滚动重启或无损重启。独立滚动重启 MCP 的链路仍未提供、未验收。

## 6. 最终恢复结果

| 项目 | 基线 | 最终 |
| --- | --- | --- |
| Chart | magic-app:2.2.8 | magic-app:2.2.8 |
| Ready / 期望 | 1/1 | 1/1 |
| Deployment UID | `cc5ff8d2-02bb-45dd-b58a-5e5695938aeb` | `df4df3a2-046d-479d-ba23-e1258991bc6d` |
| Pod | `setting-efficient-api-6cdb858d56-6nrf8` | `setting-efficient-api-6cdb858d56-qpbhg` |
| Pod UID | `1ec0dc28-6e36-42a2-b502-258691a8f2cd` | `f7fd439c-db54-4183-9ebd-3c51f9dae19a` |
| 容器 restartCount | 0 | 0 |
| Deployment spec | 基线对象 | 完全一致 |
| 实际镜像 digest | 基线 imageID | 一致 |
| 源配置 revision | `4512a44dac2f4bab0a164632cd3c2989dfe1939d` | 一致 |
| values SHA-256 | `997eb73e730c2a572be06463be0f95fc79db4a4ec17ada45b8f0cfdb36e87e68` | 一致 |
| envs SHA-256 | `3057f93021d475fddf9f3157ecb97029a94af38c828b9d5b5b94f40616fe4901` | 一致 |

安装启动时的 ContainerCreating 和重启恢复时的暂未就绪均已在本轮观察内收敛，不留作失败项。未执行业务 HTTP 探活，最终结论限于部署、容器和配置一致性；不由 Ready 推断开发者网络或业务功能已验收。

## 7. MCP 请求摘要

本轮真实写操作总共 4 次，均由平台 MCP 提交：

```json
{"name":"scale-service","arguments":{"serviceId":"2963078505","curEnv":"sim","deploymentName":"setting-efficient-api","replica":0}}
```

```json
{"name":"scale-service","arguments":{"serviceId":"2963078505","curEnv":"sim","deploymentName":"setting-efficient-api","replica":1}}
```

```json
{"name":"deploy-service","arguments":{"serviceId":"2963078505","curEnv":"sim","operate":"uninstall"}}
```

```json
{"name":"deploy-service","arguments":{"serviceId":"2963078505","curEnv":"sim","operate":"install","chartName":"lucy-chart-release/magic-app","chartVersion":"2.2.8"}}
```

实际运行的 MCP 查询工具包括 `query-service-deploy-status`、`query-service-diagnostics`、`query-service-config`、`query-service-config-context`、`query-namespace`、`query-chart-deploy-context`，以及上述配置转换和预览工具。`tools/list` 用于确认能力，不将工具发现当作功能验收。

## 8. 不在本轮范围内

- 既有八个 `k8sctl-` 日志、命令、终端、文件正常流程，未重复测试；既往结果见 [真实链路验收](publish-shell-flow-2026-11-13.md)。
- Higress 端口台账、预占、同步、释放，均未调用。服务自身 Ingress 的创建/删除视图是生命周期回读的一部分，不是端口台账测试。
- 独立滚动重启、持续终端跨 Pod 重建、跨用户权限、大文件、并发和多集群压力场景。
- magic-v2 专用部署归档工具：本服务是 magic，没有用不匹配的归档查询制造无关失败。
- Chart、源配置、镜像及控制器权限策略修改，均未执行。

## 9. 证据与交付

原始 MCP 报文、参数、时间、状态 YAML 和本地断言仅保存在 Git 忽略目录 `.local/platform-integration/lifecycle-2963078505-2026-11-14/`，目录权限 0700、证据文件 0600。`probe.py` 每次使用 initialize + tools/call；`wait_state.py` 只做状态回读；`calls.jsonl` 为调用摘要；`verified-summary.json` 为已完成断言的汇总。

关键证据文件：`baseline.json`、`restart-stopped-verified.json`、`restart-ready-verified.json`、`uninstall-request.json`、`uninstall-absent-verified.json`、`config-after-uninstall.json`、`install-request.json`、`install-in-flight.json`、`install-ready-verified.json`、`final-status.json`、`preflight-preview.json`。

证据不含认证 Header；报告不复制 kubeconfig、密钥、Token 或配置明文。原始配置文件正文不进入可提交文档。

本轮没有修改 Go/Java 业务代码，因此没有重跑与本次真实操作无关的单元测试和编译；通过本地解析断言检查源哈希、UID、Ready、镜像 digest 与 Deployment spec。没有提交或推送 Git。
