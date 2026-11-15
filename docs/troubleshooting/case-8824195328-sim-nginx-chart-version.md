# 实际排查案例：Nginx 配置文件异常与 Chart 版本能力不匹配

> 案例编号：K8SCTL-CASE-20261114-001  
> 排查日期：2026-11-14，Asia/Shanghai  
> 适用对象：通过 publish MCP 排查服务的 AI Agent、开发人员、运维人员  
> 案例状态：已定位应用启动失败，已验证新版本相关能力；升级建议已给出，尚未执行线上修复验收。

## 1. 先读结论与适用范围

服务 `8824195328` 的仿真环境中，Nginx 加载 `/etc/nginx/nginx.conf` 时报告 `unknown directive "|"`，进程退出，Pod 持续 CrashLoopBackOff。当前源配置使用 `fileType: raw` 和多行文件正文，但实际部署的应用 Chart 为 `magic-app:2.2.7`。后续核对发现，已有的 `magic-app:2.2.8` 新增了所需的 raw 文件输出能力；使用其包内真实 ConfigMap 模板进行最小离线渲染，未再产生额外竖线。

本案例的推荐处理是：**先核对源配置和实际部署版本，再比较已发布 Chart 的能力差异，验证后选择兼容版本升级。** 平台 Chart 按经过验证的部署制品使用。若配置、版本适配和部署一致性核查仍不能解释故障，由运维继续核查 Chart 与发布链路。

证据边界：应用日志直接证明了启动失败；本地渲染验证解释了多余字符的产生机制及 2.2.8 raw 分支的行为。未读取到运行容器中的实际文件，未逐字核对线上 2.2.7 完整包，未对 2.2.8 完整应用 Chart 执行正式全量渲染，未完成线上升级。不能将本案例描述为“已经升级并恢复”。

本案例适用于“应用启动即退出、日志指向挂载配置语法、服务配置与 Chart 能力可能不匹配”的情况。`CrashLoopBackOff` 本身不代表同一种根因；日志出现 OOM、连接失败、镜像拉取失败或权限错误时，应按对应证据切换分支。

## 2. 背景与目标

用户请求：排查服务 `8824195328` 仿真环境异常，同时验证现有 MCP 是否足以排查实际应用问题。

| 项目 | 本次实际信息 |
| --- | --- |
| 服务 ID / 名称 | `8824195328` / `testpy-devops-api` |
| 环境 | `sim`，仿真环境；本次必须显式指定，不能使用工具默认的 `dev` |
| 平台身份 | `10121993`；由认证上下文取得，不作为可伪造的身份参数 |
| 集群分组 / 集群 | `lucy-mid-k8s` / `gdl-dev-lucy` |
| Namespace | `devops-efficient-test1`，由平台服务信息解析 |
| Deployment / 期望副本 | `testpy-devops-api` / `2` |
| 实际镜像 | `harbor.gtjaqh.net/infra/nginx:mainline` |
| 部署模式 | `magic-v2`，源文件为 `values-desc`、`envs-desc` |
| 已部署 Chart | `lucy-chart-release/magic-app:2.2.7`，经配置上下文的 Helm release 来源确认 |
| 配置文件 / 挂载 | `/etc/nginx/nginx.conf`；ConfigMap + `subPath`；只读 |
| 状态采样时间 | 2026-11-14 11:17:02；后文状态与计数均为排查快照 |

本次通过上层 publish 的 MCP 入口查询服务，由 publish 解析服务到集群、Namespace、Pod 的映射，再调用 k8s-ctl。所有集群观察和容器请求均经 MCP 完成。后续 Chart 包检查与最小渲染为本地离线分析，应与 MCP 调用分别记录。

本次没有重启、发布、扩缩容、写入容器或修改 ConfigMap；没有拉取新镜像、安装容器工具或创建调试 Pod。

## 3. 现象与异常关键字

### 3.1 用户可感知的现象

服务没有就绪副本；Deployment 滚动更新未完成；Pod 不断重启。Pod 的 `phase=Running`，但其中业务容器正在等待重启，不能据此认定应用可用或 Shell 可进入。

| 关键字 / 字段 | 本次观测 | 应如何解释 |
| --- | --- | --- |
| `CrashLoopBackOff`、`BackOff` | 三个业务 Pod 均出现 | 反复启动失败后的退避状态，继续取容器日志找原因 |
| `unknown directive "\|"` | Nginx 的 `emerg` 错误 | Nginx 配置正文出现非法指令，是直接故障证据 |
| `/etc/nginx/nginx.conf:3` | 错误文件及行号 | 定位文件来源、挂载及生成路径 |
| `Error`、`exitCode=1` | 上次容器终止状态 | 进程启动失败；本次没有 OOMKilled 证据 |
| `MinimumReplicasUnavailable` | `Available=False` | 可用副本不足，是故障结果 |
| `ProgressDeadlineExceeded` | `Progressing=False` | 发布进度超时，是故障结果，不是 Nginx 根因 |
| `[conflict] HTTP 409` | 尝试读取容器文件失败 | 目标容器未运行，未成功执行；与此前排查过的 403 权限错误不同 |
| MCP `connection refused` | 调查中途短暂出现 | 本地 publish 入口不可达；与应用 Nginx 配置错误分开记录 |

日志原文：

```text
nginx: [emerg] unknown directive "|" in /etc/nginx/nginx.conf:3
```

### 3.2 运行状态快照

Deployment：`desired=2`、`ready=0`、实际 Pod 数 `3`、`updatedReplicas=1`。滚动策略为 `maxSurge=25%`、`maxUnavailable=25%`，旧、新模板的 Pod 同时存在。实际 3 个 Pod 不表示期望副本被修改为 3。

| Pod | Ready | 等待原因 | 上次终止 | 采样重启次数 |
| --- | --- | --- | --- | --- |
| `testpy-devops-api-5bf64f8b86-h9k5z` | false | CrashLoopBackOff | Error / 1 | 18553 |
| `testpy-devops-api-5bf64f8b86-mcgth` | false | CrashLoopBackOff | Error / 1 | 4788 |
| `testpy-devops-api-b76496bc5-gpt8t` | false | CrashLoopBackOff | Error / 1 | 4790 |

三个容器的上一轮启动均在同一秒内结束；探针初始延迟为 10 秒。因此，现有证据支持“应用启动时自行报错退出”，不支持“该轮被 10 秒后的探针杀死”。此判断限于本次采样，不能泛化为所有重启都与探针无关。

## 4. MCP 工具、调用内容与优先级

以下为 publish 对外工具名。重做案例时先通过 `tools/list` 确认工具存在及输入 Schema，再按当前 Schema 传参。工具会演进，不能照抄本案例假定所有环境都具备相同能力。

所有调用先检查 MCP `isError`，再检查平台 `code=0`。校验报告还需检查 `valid` / `passed`；命令还需检查 `outcome`、`timedOut`、`upstreamError`。协议成功不等于命令成功、配置正确或应用已恢复。

| 优先级 | 工具 / 能力 | 本次用途与状态 | 完成条件 / 限制 |
| --- | --- | --- | --- |
| P0 | `query-service-deploy-status` | 已调用；定位服务环境、集群、Namespace，读取 Deployment/Pod YAML 与事件 | 解析容器状态、conditions、挂载和版本，不能只读摘要或 phase |
| P0 | `query-service-diagnostics` | 已调用；查看就绪异常和 BackOff 事件 | 作为索引，不代替容器日志；本次摘要字段不够完整 |
| P0 | `k8sctl-get-service-logs` | 已调用；三个异常 Pod 均读取 `previous=true` | 取得直接报错；检查目标容器、截断标记与日志对应轮次 |
| P1 | `query-service-config-context` | 已调用；确认 magic-v2、文档类型，`includeChart=true` 确认 2.2.7 | 已部署 Chart 与推荐版本分别记录 |
| P1 | `query-service-config` | 已调用；读取 sim 的 values-desc、envs-desc | 两份文件固定同一 revision；明确源文档与最终 values 的区别 |
| P1，按需 | `query-service-config-history` | 已调用；核对配置历史及源版本 | 历史不自动恢复；分页沿用首次 revision |
| P1 | `query-chart-list` | 后续标准流程应调用；本案候选列表已从配置上下文取得 | 查询已有版本与推荐版本，随后比较实际版本和候选版本，不能只看最新版 |
| P1 | `query-chart-deploy-context` | 已调用；分别核对 2.2.7、2.2.8 的准确包版本和字段定义 | 返回根目录默认配置及说明，不返回模板，不执行 Helm 渲染 |
| P1，按需 | `query-chart-values-schema` | 可用的字段说明入口；本案通过 deploy-context 已取得说明 | 可核对字段，不能据此断言渲染正确 |
| P1，按需 | `query-service-config-references` | 涉及变量时使用；非本次竖线现象的主要证据 | 只核对引用元数据与目标环境存在状态，不需要展开密钥值 |
| P2 | `check-service-config-render` / `preview-service-deploy` | 本次核对了实现边界；后续可用于升级前预检 | 当时实现检查变量、YAML及部分 Chart 定义；均不执行 Helm 渲染 |
| P2 | 完整 Chart 版本差异、正式渲染、release manifest | 本次读取本地包并做最小离线验证；未完成正式全量渲染 | 当时未发现能直接返回完整模板/manifest 的 publish MCP；通过运维或已有受控能力取得，不编造工具名 |
| P3 | 服务范围内实时资源读取 | 按需核对 ConfigMap、部署后漂移 | 本次未发现对应 publish 工具；不是读取源配置和版本差异的前置条件 |
| P3 | `k8sctl-exec-service-command` | 已尝试只读查看 nginx.conf，返回 409 | 容器稳定运行且确需现场文件时使用；失败后回到日志、配置和版本证据 |
| P4 | 持续终端、文件下载、更多现场诊断 | 本次未用于此故障 | 只有前序证据不足且现场可访问时才使用，不因工具存在而全部调用 |
| 变更阶段 | `deploy-service` | 本次未调用 | 仅在升级范围、版本、授权明确后使用；完成调用仍须回读验收 |

优先级随证据调整：镜像拉取失败时先看事件，进程退出时先看 previous 日志，应用能运行但文件与预期不同才提高现场读取优先级。持续终端通常不是启动失败的首选工具。

### 4.1 可复用的请求示例

以下是本案目标的调用示例，不是完整 JSON-RPC 传输报文，也不声称每条都是原始请求的逐字转录。认证沿用 MCP 连接，不传 token 或 userId 字段。

确认服务运行状态与配置上下文：

```json
{"name":"query-service-deploy-status","arguments":{"serviceId":"8824195328","curEnv":"sim"}}
```

```json
{"name":"query-service-config-context","arguments":{"serviceId":"8824195328","curEnv":"sim","includeChart":true}}
```

读取同一版本的两份源文件：

```json
{"name":"query-service-config","arguments":{"serviceId":"8824195328","curEnv":"sim","types":["values-desc","envs-desc"],"revision":"4512a44dac2f4bab0a164632cd3c2989dfe1939d"}}
```

读取一个已确认异常 Pod 的上一轮日志；有多个容器时补充从状态取得的 `container`，多候选时按工具要求完成选择：

```json
{"name":"k8sctl-get-service-logs","arguments":{"serviceId":"8824195328","env":"sim","pod":"testpy-devops-api-b76496bc5-gpt8t","previous":true,"tailLines":200}}
```

比较准确 Chart 版本：

```json
{"name":"query-chart-deploy-context","arguments":{"chart":"lucy-chart-release/magic-app","chartVersion":"2.2.7"}}
```

```json
{"name":"query-chart-deploy-context","arguments":{"chart":"lucy-chart-release/magic-app","chartVersion":"2.2.8"}}
```

注意参数差异：配置/状态工具使用 `curEnv`，k8sctl 日志与命令工具使用 `env`。环境缺失时先确认用户意图，不能在本案中退回 dev。

## 5. 本次实际排查过程

以下按调查顺序记录，包括中途错误判断及后续纠正。顺序为事实记录，不是推荐执行顺序。

### 阶段一：确认对象、状态和直接报错

1. 通过 publish MCP 的初始化、工具发现和工具调用查询服务 sim 状态，解析出集群、Namespace 和 Deployment。没有让用户自行猜测 Namespace，也没有使用本机默认 kubeconfig 替代平台服务映射。
2. 状态、诊断与 YAML 表明：2 个期望副本均未就绪，三个 Pod 处于 CrashLoopBackOff，Deployment 超时。检查 `lastState`、退出码和时间，未发现该轮 OOMKilled 或探针杀进程的证据。
3. 对三个异常 Pod 读取上一轮日志。三个响应均未截断，均包含相同的 Nginx 配置非法竖线错误。由此将调查收敛到配置文件内容及其来源，而不是继续泛查网络、CPU、节点或认证。

阶段产出：确认应用启动失败的直接原因、错误文件和行号；尚未确认竖线来自哪个配置生成环节。

### 阶段二：追踪文件来源，遇到不可进入的容器

1. 从 Pod 挂载定义确认 nginx.conf 来自 `testpy-devops-api-nginx-cfg-cm`，通过 `subPath: nginx.conf` 挂到 `/etc/nginx/nginx.conf`，只读。
2. 尝试通过 `k8sctl-exec-service-command` 只读查看该文件，返回 `[conflict] HTTP 409`。当时容器未处于可执行状态，没有取得文件内容。
3. 初次调查过早把“没有实时 ConfigMap 读取工具”当成主要障碍，未充分利用服务部署源和准确 Chart 信息。用户指出：平台已有 values-desc、envs-desc，应该先使用这些资料。

阶段纠正：容器崩溃不妨碍读取 previous 日志和部署源；ConfigMap 现场读取不是继续推理的必要前提，409 也不能被误报为权限 403。

### 阶段三：补齐源配置、环境描述与准确 Chart

1. 使用 `query-service-config-context` 确认模式是 magic-v2。最初按 `values` 类型读取被拒绝后，改用正确的 `values-desc`；类型不匹配属于调用参数问题，不是应用故障。
2. 在同一仓库 revision `4512a44dac2f4bab0a164632cd3c2989dfe1939d` 下读取 values-desc、envs-desc，区分文件正文和变量上下文。values-desc 的 `lastCommitId` 为 `1833102a575e7656155436142a1abd69c0dd9cc1`。
3. 发现 `service.configmaps[0]` 已包含 `fileType: raw` 和多行 Nginx 文件正文。解析后的字符串以 `events {` 开始，没有单独的竖线指令。`fileContent: |` 是源 YAML 的合法写法。
4. 配置上下文通过 Helm release 确认已部署 Chart 为 `lucy-chart-release/magic-app:2.2.7`，与平台记录一致。查询其字段说明，确认 fileContent 为字符串；当时未找到 fileType 定义。
5. 核查工具边界：Schema/defaults 查询不返回 templates；名称带 render/preview 的预检也不等于执行 Helm 渲染。缺少根目录 values.yaml 的标记不等于整个 Chart 不支持部署。
6. 调查期间本地 publish 一度连接拒绝，随后服务恢复可访问。没有把入口短暂不可达归因于 Nginx，也没有通过直连 Kubernetes 绕过 MCP 完成集群调查。

阶段产出：源配置看起来有意使用 raw 能力，需要继续核对实际版本是否支持；已有候选版本列表，但当时没有及时比较 2.2.8。

### 阶段四：本地离线分析解释字符来源，但建议过早转向开发

1. 在本地找到版本标注为 2.2.7 的库 Chart，最小离线渲染复现了配置正文额外出现竖线的机制。本地复现的竖线位于正文第 2 行，运行日志为第 3 行；不能声称运行文件逐字复现。
2. 模板副本实验能够消除额外字符，但这只是机制验证，不能证明修改 Chart 是首选处理方式。原 Chart 源码及集群均未修改。
3. 当时给出了自行调整 Chart 并另发版本的建议，漏掉了“先比较现有已发布版本”的步骤。该建议随后被纠正，不作为本案例的推荐解决方案。

本阶段保留的经验：本地同版本号库不一定等于线上应用包；本地应用仓库当时为 3.0.0-alpha，更不能代替线上 2.2.7。离线实验可以解释机制，但不能替代制品核对与现场恢复验收。

### 阶段五：用户指出 2.2.8，补查并修正解决建议

1. 用户指出 2.2.8 已支持多行配置。通过 MCP 精确查询 `magic-app:2.2.8`，返回包版本 2.2.8，字段说明增加 `service.configmaps[].fileType`。
2. 读取本机缓存的 `magic-app-2.2.8.tgz`，确认其中依赖和打包库均为 2.2.8；包内实际实现具有 raw 文件直接输出分支。
3. 将包内真实 ConfigMap 模板放入最小离线 Chart，使用服务当前的 raw Nginx 配置执行 Helm 渲染。解析后的文件正文与源字符串一致，没有额外竖线。
4. 修正建议：优先验证并采用已有的 2.2.8，保留合法的 raw 源配置。没有执行线上升级，也没有宣布应用恢复。

阶段产出：已验证一个与现有配置匹配的已发布版本能力；剩余工作是完整升级预检、获授权后的升级及恢复验收。

## 6. 本次应检查的配置与版本内容

以下片段为源配置中的非敏感相关字段，仅展示问题关联内容，不是可直接部署的完整 values 或 nginx.conf：

```yaml
service:
  configmaps:
    - name: nginx-cfg
      readOnly: true
      containerFileName: nginx.conf
      containerPath: /etc/nginx
      fileType: raw
      fileContent: |
        events {
          worker_connections 1024;
        }
        http {
          # 本案例省略其余合法配置
        }
```

| 核对对象 | 要回答的问题 | 本次结论 |
| --- | --- | --- |
| values-desc | 挂载文件内容、路径、类型如何声明？ | raw + 合法多行字符串，正文不含单独竖线 |
| envs-desc / 引用上下文 | 哪些变量会影响最终文件、镜像和探针？ | 已补查同 revision；没有把源描述文件冒充已解析 values |
| 当前 Chart 2.2.7 | 实际部署版本是否实现所使用字段？ | 线上已确认版本；本地同版本库没有 raw 分支，线上包仍未逐字核对 |
| 已有 Chart 2.2.8 | 是否有可直接复用的匹配能力？ | 字段说明及本地缓存包均有 raw 能力；最小渲染验证通过 |
| Pod spec / 挂载 | 应用究竟读取哪个文件，是否只读或旧挂载？ | ConfigMap + subPath + 只读，需关注 Pod 重建 |
| 镜像 / command / args | 是否部署了意外镜像或覆盖启动命令？ | 实际为 Nginx 镜像，没有 command/args 覆盖证据 |
| 探针与资源限制 | 退出是否来自探针或 OOM？ | 本次时间与退出原因不支持这两个方向 |

多行文件正文与 YAML 文档对象应按准确版本的使用说明配置。出现非法竖线，不意味着应删除源文件的 `|` 标记。也不能因为 Schema 出现某字段，就宣称该版本的全部运行行为已验证。

## 7. 推荐给后续 AI 的排查路径

### 第一步：确认目标与现有知识

确定 serviceId、明确环境，读取适用的服务状态查询和配置查询 Skill；涉及发布时再进入部署 Skill。本次初始调查没有完整遵循配置排查路径，文档记录的是复盘后的改进流程，不声称最初已正确执行。

完成条件：服务、环境唯一；工具存在；认证及服务访问范围明确；后续调用显式指定目标环境。

### 第二步：用状态和日志区分故障类型

解析 Deployment conditions、Pod 容器状态、上次退出原因、事件和当前/上一轮日志。按日志选方向：配置语法错误查配置，OOM 查内存证据，镜像问题查拉取事件，依赖问题查配置及连接证据。

完成条件：至少找到能解释异常状态的直接证据，或明确哪些证据尚缺。BackOff、NotReady、发布超时只能作为现象，不能单独充当根因。

### 第三步：先读取部署源，再确认准确版本

按服务模式读取正确文档类型；magic-v2 同时核对 values-desc、envs-desc，固定 revision。确认实际部署 Chart 及其来源，再查看该准确版本的字段说明、使用方式和已发布版本列表。

完成条件：能够说明出错文件如何声明、有哪些变量参与，以及当前 Chart 对相关字段的支持情况。配置读取失败、缓存为空、默认值缺失分别记录。

### 第四步：优先比较已有 Chart 版本

将当前配置需要的能力与当前版本、相邻可用版本逐项对照。查阅版本说明、运维提供的使用文档或准确包中的字段定义；必要时请运维确认完整制品差异。版本号高不自动意味着合适，应验证与目标集群和现有配置兼容。

完成条件：给出“哪些字段或行为不同、哪个版本满足需求、依据来自哪里”，或明确现有资料不足。本案的关键差异是 2.2.8 对 raw 多行文件的支持。

### 第五步：验证生成结果，按需核对现场

采用正式变量解析和准确版本 Chart，检查渲染后的完整 manifest。若预期正确而运行仍异常，再核对当次 Helm release manifest、当前资源和运行文件，排查部署版本、配置 revision、人工变更或旧挂载差异。

完成条件：明确区分“源配置”“渲染预期”“当次 release 记录”“实时资源”“容器文件”五层证据。最小渲染通过不能代替正式全量渲染；工具缺失时向运维说明所需资料。

### 第六步：提出最小范围建议并验收

优先选择正确配置、匹配的已验证 Chart 版本或运维修正的部署流程。诊断请求本身不授权升级、回滚、重启或卸载。涉及变更时明确目标和授权，调用结果未知先回读，不自动重试写操作。

完成条件：方案、风险、验证和未完成项齐全；实施后才能依据新证据报告恢复。若前述检查仍无解释，将疑似 Chart 问题及证据移交运维，不由 AI 默认开展 Chart 源码改造。

## 8. 最终解决建议

### 8.1 本次首选方案：验证后升级到已有 2.2.8

1. 保留服务已有 `fileType: raw` 及完整多行 Nginx 配置，使用最新回读的源版本确认没有并发变化。
2. 核对平台可用的 `lucy-chart-release/magic-app:2.2.8`，按正式流程解析配置并检查完整 manifest。重点查看 nginx.conf 正文，以及镜像、探针、资源、路由、挂载等是否出现意外变化。
3. 在授权范围内只升级服务 `8824195328` 的 sim 环境，不因本案修改其他服务或环境。
4. 确认本次升级实际产生新 Pod。当前使用 subPath，旧 Pod 不会接收 ConfigMap 更新；仅更新配置对象不足以保证运行文件更新。若发布没有触发滚动，由运维按授权安排处理。
5. 完成下述验收。若升级后仍异常，回读新 Pod 的日志和状态，确认是否仍为同一错误，再决定后续方案。

本次只验证了 2.2.8 的相关 ConfigMap 输出分支，不保证升级后所有业务行为都正确。版本变更还需核对完整部署差异。

### 8.2 仍无法解决时：交由运维核查

满足以下情况之一时联系运维：匹配版本仍生成异常正文、发布包与文档行为不一致、完整渲染正确但当次 release 内容不同、资源/挂载与发布预期不一致，或缺少取得准确制品和渲染证据的能力。

移交材料包括：服务与环境、故障时间、准确 Chart 名称/版本及依赖、配置 revision、必要的非敏感配置片段、报错日志、Pod 状态、已比较的版本差异、渲染或现场差异，以及已完成和未完成的验证。标注“疑似 Chart 或部署链路问题，待运维确认”，不将未经核实的实现推断定性为线上 Chart 缺陷。

恢复策略由运维结合发布记录评估。若考虑回退，先确认目标版本与配置兼容，不能假定回退到不支持 raw 的版本会解决问题。

### 8.3 恢复验收清单：本案尚未执行

- Deployment 期望 2、Ready 2，发布进度正常，旧、新 Pod 的替换符合滚动策略。
- 新 Pod 实际使用目标 Chart 生成的资源和预期配置，挂载文件不再出现异常竖线。
- 新容器日志没有同一 `unknown directive` 错误，观察窗口内没有新增异常重启；记录窗口与采样时间，不凭单次 Running 判断恢复。
- 服务健康检查及业务访问符合预期；HTTP 成功与业务功能正确分别验证。
- 记录最终 Chart 版本、配置 revision、Pod 身份和验证结果，才能将案例状态改为“已恢复”。

## 9. 排查中的误区与修正

| 本次误区 | 后续采用的规则 |
| --- | --- |
| 先强调缺少 ConfigMap 工具 | 先使用已有部署源、准确版本和渲染证据，按需读现场 |
| 初期未补齐 envs-desc 和准确 Chart 定义 | 两份源文件同 revision，版本按来源确认 |
| 过早提出 Chart 开发方案 | 先查已发布版本差异，本案已有 2.2.8 可复用 |
| 将同版本号本地库视为完整线上制品 | 同时核对应用包、依赖和来源，保留证据缺口 |
| 把 Schema/preview 当成渲染 | 分别记录字段说明、预检、真实渲染、部署和 Ready |
| 容器不可进入后继续依赖 Shell | 改用 previous 日志、状态和部署源推进 |
| 因报错有竖线而怀疑所有 YAML 块字符串 | 比较解析后的正文，保留合法 YAML 写法 |
| 把 409、本地入口拒绝与此前 403 混为一谈 | 按认证、传输、目标状态、应用错误分层记录 |

## 10. 后续 AI 的输出格式

复用本案例时，按以下字段形成新的调查报告，不直接复制本案根因：

```text
目标：服务、环境、集群/Namespace 的来源、采样时间。
现象：用户影响、Deployment/Pod 状态、直接报错。
背景：镜像、配置模式、实际 Chart 与配置 revision。
证据：工具、必要参数、关键返回、证据来源及限制。
过程：每一步假设、验证结果、失败调用和下一步理由。
版本比较：现用版本、候选版本、相关能力差异和依据。
判断：已确认 / 证据支持但待核对 / 已排除 / 未验证。
建议：优先配置与匹配版本；仍异常时提供运维移交资料。
验收：明确操作、授权、实际结果与尚未完成的项目。
```

## 11. 证据索引与复用说明

详细原始调查记录见 [服务 8824195328 仿真环境排查记录](../testing/service-8824195328-sim-diagnosis-2026-11-14.md)。该记录保留当时建议及修正；后续 AI 选择排查路径和解决建议时，以本案例的推荐流程为准。

原始 MCP 响应和离线材料保存在 Git 忽略的 `.local/platform-integration/diagnosis-8824195328-2026-11-14/` 中，包含 `status-confirmed.json`、三个 `logs-*.json`、配置及 Chart 查询响应，以及 `chart-repro/2.2.8-verification.json`。这些私有材料不随本文交付，后续环境不能假定可访问；它们也不是必须重新调用所有工具的清单。

最小离线验证记录为：`fileType=raw`、`contentMatch=true`、`extraPipe=false`。该结果的范围是包内模板分支，不能被引用为真实集群恢复证据。

本文仅保留必要的非敏感上下文和日志；未记录 kubeconfig、token、证书、连接凭据或环境变量值。工具返回正文和本地缓存包应在授权范围内读取与保存。

技术参考：[ConfigMap 的 subPath 挂载更新限制](https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/#mounted-configmaps-are-updated-automatically)。
