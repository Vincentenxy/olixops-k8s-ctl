# publish / k8s-ctl 权限实测：10121993

测试日期：2026-11-14。结论：真实用户授权已生效，允许和拒绝分支均有实测证据；不能据此宣称所有环境和全部权限边界已验收。本文仅测试和记录，不改变权限策略、授权关系或业务部署。

## 结果概览

正式清单共 **41 项：34 PASS、3 FAIL、4 BLOCKED**。FAIL 中两个场景复现同一个 HTTP 状态码问题，另一个场景复现能力声明与执行结果不一致；拒绝行为本身没有被绕过。配置保存工具另有结构化错误格式缺口，见下文。

补充执行现有 Java 权限/认证自动化测试：**166 项，0 失败、0 错误、0 跳过**，包括 9 项使用临时独立 Redis 的 PAT Servlet 集成测试。它们是自动化证据，与真实用户和集群结果分开统计。

## 环境与可信身份

- 上层：`/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api`，IDEA 启动实例，HTTP `http://127.0.0.1:9001`，MCP `/pubops/mcp`。
- 下层：本地 HTTP 操作器 `http://127.0.0.1:19091`，由 publish 解析服务、环境后调用。没有直接使用本地 standalone 8091 的另一个集群替代验收。
- 现有 PAT 认证记录只读核对：`userId=10121993`、`staffNo=121993`、`status=ACTIVE`。权限中心 USER 主体使用 **121993**；k8s-ctl 接收可信平台签发的用户 ID **10121993**。
- `authz.enabled=true`、`service-detail-enabled=true`；能力查询实测返回 `enabled=true`。未关闭鉴权、未新建 Grant、未冒充管理员。
- 主正向目标：`2963078505 / setting-efficient-api / sim / gdl-dev-lucy / setting-efficient`。负向目标：`3899597151 / gxj-adm-api`，使用其真实现有授权。
- 日志、原始返回和测试脚本保存在 gitignored 私有目录 `.local/platform-integration/publish-permissions-10121993-2026-11-14/`；未把 PAT、Source Credential、kubeconfig、完整日志或业务配置正文写入本文。

## 当前用户的真实权限差异

| 服务 / 环境 | 已观察允许 | 已观察不允许 / 不可用 | 说明 |
| --- | --- | --- | --- |
| 2963078505 / sim | 日志、终端、配置、基本信息、运维文档等能力查询均允许；8 个 k8sctl MCP 代理均完成正向调用 | 本次未找到该目标的 Grant 拒绝动作 | 不能用它构造无权限测试 |
| 3899597151 / sim | 日志、配置读取、状态、事件等 | 基本信息、运维文档、配置修改、部署、扩缩容等能力查询不允许 | 文档读取和配置保存入口已实际拒绝；未执行卸载等破坏性请求 |
| 3899597151 / prod | 概览、源配置读取/修改、状态、重启 | 日志、终端、运行态 YAML、解析配置等 | HTTP 日志入口已验证权限拒绝；MCP 另有生产环境全局禁止策略 |
| 3899597151 / dev、test | 环境能力采用开放策略 | 共享基本信息/运维文档的执行仍按原权限拒绝；LQY 终端不支持 | 能力查询误把共享操作声明为允许，见问题 2 |

`TERMINAL=false` 不总是“用户没有终端 Grant”：源码 `supported()` 会将 LQY 原生终端标为不支持。此服务的终端/文件工具又先遇到 `backend_not_configured`，因此这四项标为 BLOCKED，不作为权限中心 DENY 的成功证据。

## 真实调用清单

| 编号 | 场景 | 服务 / 环境 | 入口 | 观察结果 | 结论 | Request ID |
| --- | --- | --- | --- | --- | --- | --- |
| P01 | 有权限：读取服务基本信息 | 2963078505 /  | /api/v1/service/query/id | 业务 code=0 | PASS | authz-10121993-ccf4707ff17f4c |
| P02 | 无权限：读取服务基本信息 | 3899597151 /  | /api/v1/service/query/id | 已拒绝；业务 code=403，但 HTTP 500，应返回 403 | FAIL | authz-10121993-917a44c41ed740 |
| P03 | 有权限：读取运维文档 | 2963078505 /  | query-service-operations-document | code=0；正文仅保存于私有证据 | PASS | authz-10121993-7771a039492a47 |
| P04 | 无权限：读取运维文档 | 3899597151 /  | query-service-operations-document | isError=true，code=403；未返回正文 | PASS | authz-10121993-2c2409f4e00d42 |
| P05 | 无权限：保存配置；使用不可提交的空草稿和空提交说明 | 3899597151 / sim | save-service-config | 权限拒绝先于参数校验；SDK 工具错误正文为未授权；未提交配置 | PASS | authz-10121993-0f238b18b8f845 |
| P06 | 有权限：保存入口通过授权后执行参数检查 | 2963078505 / sim | save-service-config | 返回提交说明不能为空；仅验证执行顺序，没有实际保存 | PASS | authz-10121993-40d66619bb3548 |
| P07 | dev 开放策略：配置保存入口 | 3899597151 / dev | save-service-config | 通过环境策略后被空提交说明阻止；未保存 | PASS | authz-10121993-e59709f2a42c4a |
| P08 | HTTP：不携带身份 | 2963078505 / sim | /api/v2/service/authorization/capabilities | HTTP 401，code=401 | PASS | authz-10121993-8ad89f4a034642 |
| P09 | HTTP：无效 PAT | 2963078505 / sim | /api/v2/service/authorization/capabilities | HTTP 401，code=401 | PASS | authz-10121993-cae915158bf244 |
| P10 | MCP：不携带身份 | 2963078505 / sim | k8sctl-get-service-logs | 初始化被 HTTP 401 拒绝 | PASS | authz-10121993-88741ec97b1046 |
| P11 | MCP：无效 PAT | 2963078505 / sim | k8sctl-get-service-logs | 初始化被 HTTP 401 拒绝 | PASS | authz-10121993-ecceef37cf664f |
| P12 | PAT 不变，伪造用户/员工号 Header | 3899597151 /  | query-service-operations-document | 仍然拒绝原本无权读取的文档，code=403 | PASS | authz-10121993-b5b21ea4e6d64b |
| P13 | MCP 添加 namespace/clusterId/userId 参数 | 2963078505 / sim | k8sctl-get-service-logs | 参数被忽略；仍访问平台解析出的 setting-efficient / gdl-dev-lucy；未越界 | PASS | authz-10121993-cc31ca6dc3c346 |
| P14 | 把另一服务实际 Pod 传给日志工具 | 2963078505 / sim | k8sctl-get-service-logs | forbidden；下游只有候选清单查询，没有日志调用 | PASS | authz-10121993-64424e07e06743 |
| P15 | 把另一服务实际 Pod 传给 Shell 工具 | 2963078505 / sim | k8sctl-exec-service-command | forbidden；下游没有 exec 调用 | PASS | authz-10121993-c5c1b9c92dd248 |
| P16 | 传入不属于目标 Pod 的容器名 | 2963078505 / sim | k8sctl-get-service-logs | invalid_argument；下游没有日志或 exec 调用 | PASS | authz-10121993-785702dd6a7249 |
| P17 | 有权限：读取服务日志 | 2963078505 / sim | k8sctl-get-service-logs | code=0，调用到操作器日志 HTTP 接口 | PASS | authz-10121993-6f44861bd8aa4f |
| P18 | 有权限：执行固定 printf | 2963078505 / sim | k8sctl-exec-service-command | outcome=completed，输出 PERMISSION_PROBE_ALLOW，未超时、无上游错误 | PASS | authz-10121993-a333ecd4949443 |
| P19 | 有权限：打开终端 | 2963078505 / sim | k8sctl-open-service-terminal | 返回本人 sessionId | PASS | authz-10121993-fac4ba8e210447 |
| P20 | 有权限：向本人终端输入固定 printf | 2963078505 / sim | k8sctl-write-service-terminal | code=0 | PASS | authz-10121993-2aade7bfd3d348 |
| P21 | 有权限：读取本人终端 | 2963078505 / sim | k8sctl-read-service-terminal | base64 解码后包含 PERMISSION_TERMINAL_OK | PASS | authz-10121993-6883e95e3da44d |
| P22 | 有权限：关闭本人终端 | 2963078505 / sim | k8sctl-close-service-terminal | state=closed；remoteExitConfirmed=false，不能声称远端退出已确认 | PASS | authz-10121993-c5f9e827197346 |
| P23 | 有权限：准备上传临时文件 | 2963078505 / sim | k8sctl-prepare-service-file-upload | 返回本人上传引用 | PASS | authz-10121993-66a6741456c840 |
| P24 | 有效上传引用：无身份访问 | 2963078505 / sim | /api/v1/k8s-ctl/files/6c3a96a2-817e-4ea7-9830-399645e0128c/upload | HTTP 401；同一引用随后由本人成功使用 | PASS | authz-10121993-bbc59575d44747 |
| P25 | 上传 URL：无效 PAT | 2963078505 / sim | /api/v1/k8s-ctl/files/b71d6f66-dbcb-4ba1-9fe2-da03e443ca0a/upload | HTTP 401，认证失败先于引用查找 | PASS | authz-10121993-e490f5def5d142 |
| P26 | 有权限：HTTP 上传临时文件 | 2963078505 / sim | /api/v1/k8s-ctl/files/6c3a96a2-817e-4ea7-9830-399645e0128c/upload | HTTP 200，code=0；实际写入 25 字节测试内容 | PASS | authz-10121993-698adccae0ba42 |
| P27 | 已成功使用的上传引用再次使用 | 2963078505 / sim | /api/v1/k8s-ctl/files/6c3a96a2-817e-4ea7-9830-399645e0128c/upload | HTTP 404，引用不再可用 | PASS | authz-10121993-87447868ea8049 |
| P28 | 有权限：准备下载本人测试文件 | 2963078505 / sim | k8sctl-prepare-service-file-download | 返回本人下载引用 | PASS | authz-10121993-a27ef1a78abb46 |
| P29 | 有效下载引用：无身份访问 | 2963078505 / sim | /api/v1/k8s-ctl/files/68204730-59c3-4ec8-9967-c5fa9e657984/download | HTTP 401；同一引用随后由本人成功使用 | PASS | authz-10121993-ae9310d6afee4e |
| P30 | 下载 URL：无效 PAT | 2963078505 / sim | /api/v1/k8s-ctl/files/221053be-e61e-4313-8a4c-9bb633ad677b/download | HTTP 401，认证失败先于引用查找 | PASS | authz-10121993-e219c0dea08842 |
| P31 | 有权限：HTTP 下载测试文件并核对 SHA-256 | 2963078505 / sim | /api/v1/k8s-ctl/files/68204730-59c3-4ec8-9967-c5fa9e657984/download | 字节数、SHA-256 与上传内容一致 | PASS | authz-10121993-e3500fe6c2b445 |
| P32 | 已成功使用的下载引用再次使用 | 2963078505 / sim | /api/v1/k8s-ctl/files/68204730-59c3-4ec8-9967-c5fa9e657984/download | HTTP 404，引用不再可用 | PASS | authz-10121993-3a536dd18ad646 |
| P33 | 清理本人创建的临时文件并确认不存在 | 2963078505 / sim | k8sctl-exec-service-command | outcome=completed；输出 PERMISSION_FIXTURE_REMOVED | PASS | authz-10121993-239dc86651f047 |
| P34 | MCP 生产环境全局开关 | 3899597151 / prod | k8sctl-get-service-logs | forbidden：生产环境禁止使用 MCP；此项不作为用户 Grant 拒绝证据 | PASS | authz-10121993-005ad01e61d240 |
| P35 | HTTP 日志入口：生产环境无 LOGS_READ 权限 |  /  | /api/v2/service/status/log/stream?serviceId=3899597151&curEnv=prod&podName=permission-probe-not-a-real-pod&container=gxj-adm-api&tailLines=1 | 权限拒绝生效且未读日志；业务 code=403，但 HTTP 500 | FAIL | authz-10121993-ccf5d16bbb8749 |
| P36 | 对不可用终端目标调用 Shell | 3899597151 / sim | k8sctl-exec-service-command | 先返回 backend_not_configured；无法据此验证 TERMINAL Grant 拒绝 | BLOCKED | authz-10121993-e3ea0118bb934d |
| P37 | 对不可用终端目标打开终端 | 3899597151 / sim | k8sctl-open-service-terminal | LQY 终端不支持，且目标未接控制器；先返回 backend_not_configured | BLOCKED | authz-10121993-37fa54af3b264a |
| P38 | 对不可用目标准备上传 | 3899597151 / sim | k8sctl-prepare-service-file-upload | 先返回 backend_not_configured；没有获得引用 | BLOCKED | authz-10121993-4e1cbf62682644 |
| P39 | 对不可用目标准备下载 | 3899597151 / sim | k8sctl-prepare-service-file-download | 先返回 backend_not_configured；没有获得引用 | BLOCKED | authz-10121993-322514a05e5743 |
| P40 | dev/test 能力声明与共享操作实际判权一致性 | 3899597151 / dev | /api/v2/service/authorization/capabilities | 两环境均声明 DOCUMENT_READ、SERVICE_READ=true；实际同服务共享文档/基本信息被拒绝 | FAIL | authz-10121993-7df829348ed647 |
| P41 | publish 重载后旧终端引用 | 2963078505 / sim | k8sctl-read-service-terminal | not_found；旧引用不能恢复；没有更换身份或绕过鉴权 | PASS | authz-10121993-cec8dfd2cf674f |


文件测试只在 `/tmp` 创建随机名文件，内容为固定测试字符串，实际 **25 字节**；上传后通过 publish HTTP 文件代理下载并验证 SHA-256，再通过有权限的 Shell 删除并执行 `test ! -e` 确认。未修改应用配置、镜像或部署。终端测试只执行固定 `printf`，新建有效会话已关闭；关闭返回 `remoteExitConfirmed=false`，不推定远端退出已确认。

## 未授权请求是否进入下层

每个请求使用独立 `X-Request-Id`，并与操作器 `operator-restored-http.log` 中的 `traceId` 对照。拒绝的运维文档、配置保存、无身份、无效 PAT、生产日志权限请求均没有匹配的下层调用记录。正向日志、Shell、终端和文件请求具有对应下层访问记录；Shell 审计记录用户为 `10121993`。

跨服务 Pod 和错误容器请求需要先读取**授权服务自身**的 Pod 候选清单；下层记录只包含能力查询、资源列表，没有 `/pods/logs`、`/pods/exec` 或终端调用。这证明没有向另一个服务执行命令或读取其日志，但不表示完全没有下层只读请求。

证据局限：这是本地操作器访问/执行日志与业务返回的关联核对，没有部署 SQL 探针或对所有数据库查询做逐条审计。配置保存负例还使用空提交说明作为防护，即使错误放行也会在提交前停止；因此只能证明授权先于保存参数校验，不能声称验证了真实配置写入。

## 发现的问题与处理建议

### 1. HTTP 拒绝响应的状态码不正确

`POST /api/v1/service/query/id` 和 `GET /api/v2/service/status/log/stream` 在无权限时实际返回 HTTP **500**，响应正文为 `code=403`、`msg=权限中心未授权该操作`。拒绝有效，但客户端、代理和监控会把权限拒绝识别成服务器故障。应由统一异常适配将业务 401/403/404 等映射为对应 HTTP 状态，并保持原响应结构。见 P02、P35。

### 2. dev/test 能力查询误把共享操作标为允许

`queryPageCapabilities()` 的开放环境分支对所有 `ServiceOperation` 直接返回 `supported()`，连 `environment=false` 的基本信息和共享运维文档也放行声明；执行端 `checkOperation()` 只对环境操作应用开放策略。

实测 `3899597151` 在 dev/test 的能力查询给出 `SERVICE_READ=true`、`DOCUMENT_READ=true`，但实际查询基本信息和运维文档均被拒绝。应只对环境操作套用开放策略，共享操作继续向权限中心判权；验收应将能力查询与实际执行成对比较。见 P40。

### 3. MCP 配置保存的业务错误缺少稳定结构

`save-service-config` 的无权限返回 `isError=true`，文本为“权限中心未授权该操作”，没有结构化 `code=403`；同平台运维文档工具已有 `code=403`。禁止访问仍然生效，但 Agent 需要根据文本区分权限错误和参数错误。建议复用统一 MCP 错误适配；无需把 MCP 协议响应当普通 HTTP 状态处理。见 P05、P06。

### 4. 终端/文件负向验收被后端配置检查挡在前面

`ServiceTargetResolver.resolve()` 先 `backend(cluster)`，之后 `ServiceObservationService.select()` 才 `authorize()`。目标未接入时优先暴露 `backend_not_configured`，无法观察真正的权限或能力拒绝。这不是已证明的越权执行，但错误优先级及验收覆盖需要完善。建议在可获取的真实服务目标上先完成操作授权/能力检查，再解析执行后端；补一组“已接入控制器且用户明确无 TERMINAL/文件所需权限”的目标后重测。见 P36–P39。

## 环境策略与尚未覆盖的场景

- **dev/test 目前按开放环境策略允许环境操作**，sim/prod 才逐项检查对应服务资源授权；共享服务能力另行判权。不能把开发/测试环境成功调用等同于用户获得了对应的个人 Grant。若目标是开发环境也精细控制终端、日志或写入，现策略需要调整，本轮没有擅自修改。
- MCP 的 prod 全局禁止策略先于服务授权，403/forbidden 不能自动归因于用户无权。
- 下层 `pod-exec.authorization-enabled=false`、Shell 拦截器默认放行等既有联调配置未改动。本次服务授权主要在 publish 校验，没有证明绕过 publish 直连下层也具备同等服务权限。
- 没有第二个真实用户的合法凭证，**跨用户终端/文件引用隔离没有完成本轮真实双用户联调**。伪造 Header 不是跨用户凭证测试的替代。
- 没有撤销/恢复真实用户授权，因此会话建立后撤权、Grant 变化即时生效、组继承和管理员边界没有完成真实场景测试。
- 权限中心超时/断连/异常协议的失败关闭由自动化覆盖，没有对正在使用的权限中心实施断网或故障注入。
- 初次 Shell 遇到容器未执行/HTTP 409；后来目标 Pod 已变化，复测得到 `completed`。没有为此重启或修复业务。publish 本地重载期间还观察到连接拒绝/流中断和旧引用失效；这些不计作权限拒绝。
- 测试期间第一次创建的终端引用在 publish 重载后不可再查询或关闭；未推定已从下层主动清理。正常流程后创建的会话已关闭；旧会话需由下层既有退出/超时机制回收。

## 自动化验证与复现方式

在 IDEA 的 publish 项目目录执行现有测试，不修改生产配置：

```bash
mvn '-Dtest=PublishAuthorizationServiceTest,PublishAuthorizationEnforcementTest,PublishAuthorizationWorkloadTest,AuthzClientFailClosedTest,AuthzClientHardeningTest,AuthzLookupScopeTest,RuntimeStatusAuthorizationTest,ConfigContentAuthorizationTest,ServiceTargetResolverTest,PersonalTokenAuthenticationIntegrationTest' test
mvn -Dtest=PersonalTokenAuthenticationIntegrationTest -DpersonalToken.test.redisExecutable=/opt/homebrew/bin/redis-server test
```

第一条 157 项通过、9 项条件跳过；发现本机已有 `redis-server` 后，通过第二条在随机回环端口启动临时、无持久化 Redis，9 项全部补测通过，合计 166 项通过。未连接业务 Redis 写入认证测试数据。

真实调用脚本从本地 Codex `mcp_servers.publish` 读取既有认证，仅在内存中使用；不在命令行或文档里粘贴 Token。示例（只读）：

```bash
python3 .local/platform-integration/publish-permissions-10121993-2026-11-14/probe.py call verify-denied-document query-service-operations-document '{"serviceId":"3899597151","maxChars":50}'
python3 .local/platform-integration/publish-permissions-10121993-2026-11-14/probe.py call verify-allowed-logs k8sctl-get-service-logs '{"serviceId":"2963078505","env":"sim","tailLines":1}'
```

本报告与 [CSV 清单](publish-permissions-10121993-2026-11-14.csv) 可交接；私有脚本和原始响应属于当前工作机证据，不随 Git 提交。本文未以 Mock 结果替代未完成的真实环境场景，也未执行卸载、扩缩容或重启等破坏性权限探测。
