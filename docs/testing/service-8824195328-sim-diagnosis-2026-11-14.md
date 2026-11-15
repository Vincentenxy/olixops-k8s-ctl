# 服务 8824195328 仿真环境故障排查与 MCP 能力核对

> 本文保留调查过程中的历史判断与修正。后续 AI 遇到配置启动失败、需要比较 Chart 版本或安排排查工具时，请阅读 [整理后的实际排查案例](../troubleshooting/case-8824195328-sim-nginx-chart-version.md)，以其中的优先级、版本适配方案和运维移交流程为准。

最终状态采样：2026-11-14 11:17:02（Asia/Shanghai）。本轮仅诊断，没有重启、发布、扩缩容、修改 ConfigMap、创建调试 Pod 或安装工具。

## 最新修正：优先复用已发布的 2.2.8

用户指出 2.2.8 已支持多行文件。补查确认该提醒成立：通过 MCP 精确查询 `lucy-chart-release/magic-app:2.2.8`，返回 code=0，包版本为 2.2.8，字段说明新增 `service.configmaps[].fileType`。同时读取本机 Helm 缓存中的 `magic-app-2.2.8.tgz`，确认其依赖及打包库均为 `magic-normal-service-lib:2.2.8`，实际 `_normal-configmap.tpl` 已增加以下分支：

```gotemplate
{{- $fc := dig "fileContent" (dict) $configmap -}}
{{- if eq (dig "fileType" "" $configmap) "raw" -}}
{{ $fc | nindent 4 }}
{{- else -}}
{{ toYaml $fc | nindent 4}}
{{- end -}}
```

当前服务源配置已有 `fileType: raw`。将 2.2.8 包内真实模板放入最小复现 Chart，使用当前 Nginx 正文执行离线 Helm 渲染，结果为 `contentMatch=true`、`extraPipe=false`，正文以 `events {` 开始。此测试覆盖包内 ConfigMap 模板的 raw 分支，未声称完成整个应用 Chart 的正式变量解析、全量渲染或线上验收。验证结果记录在私有目录 `chart-repro/2.2.8-verification.json`。

因此，推荐方案修正为：保留现有合法的 `fileType: raw` 与多行 `fileContent`，先用完整输入渲染并检查已发布的应用 Chart 2.2.8，再按授权升级此服务的 sim 环境，确认新 Pod、2/2 Ready 和业务可用。无须为本问题优先修改旧库再另发版本。仅升级版本仍需检查其他 manifest 差异及 subPath/滚动边界。

上一轮遗漏了可用版本 2.2.8 的能力核查，直接建议自行修 Chart 并发布新版本，属于修复方案选择不完整。下面关于旧模板的机制分析与离线复现仍成立，但“重新开发并发布新 Chart”不再是本次首选建议。未修改集群或原 Chart 源码。

## 历史建议：旧模板问题与离线验证

后续在本地 `magic-normal-service-lib-v1.24` 仓库找到版本标注为 2.2.7 的库 Chart。`templates/_normal-configmap.tpl:17` 已使用 YAML 块字符串 `|-`，第 21 行又调用 `toYaml $configmap.fileContent`。对于 Schema 定义的多行字符串，这会重复序列化，将第二层块标记写入 ConfigMap 的实际文件正文。该模板未使用 `fileType`，单独调整 `fileType: raw` 无法修正这里的输出。

在 Git 忽略目录中复制模板，建立最小 Chart，并用当前服务的 Nginx 配置执行离线 `helm template`。原模板生成的 `ConfigMap.data["nginx.conf"]` 含单独的 `|` 行；本地复现位于正文第 2 行，实际容器日志为第 3 行，未声称逐字复现运行文件。在模板副本中将第 21 行改为下面写法后，解析后的正文与输入字符串一致，仅移除外层 `|-` 预期裁去的末尾换行；没有额外竖线或前导空行。

```gotemplate
    {{- $configmap.fileContent | nindent 4 }}
```

离线验证结果：`contentMatch=true`、`leadingBlankLine=false`、`extraPipe=false`。原始模板和集群未修改，未执行 Nginx 进程级校验。私有复现材料位于 `.local/platform-integration/diagnosis-8824195328-2026-11-14/chart-repro/`。

本地库的版本号与线上应用 Chart 的版本号相同，不足以证明制品相同。正式修复前仍需核对已发布 `magic-app:2.2.7` 包内模板及其库依赖。本地 `magic-app-v1.24` 当前为 3.0.0-alpha，不能当作线上 2.2.7 制品。这里已验证重复序列化的机制和最小修法，尚未完成已部署包的逐字确认或线上修复验收。下文“未取得模板/未渲染”是此前 MCP 调查阶段的记录，应结合本节后续本地验证阅读。

建议实施顺序：

1. 对照已发布的准确 Chart 制品，确认同一缺陷；保留源配置合法的 `fileContent: |`。
2. 修复库模板，增加多行字符串回归验证；若需兼容对象类型，应单独定义兼容策略，不将当前字符串修法泛化为对象处理。
3. 发布新的库 Chart 版本，更新应用 Chart 的依赖、锁定文件及打包依赖，再发布新的应用 Chart 版本，不覆盖 2.2.7 制品。
4. 用相同 revision 的 values-desc/envs-desc 和正式变量解析逻辑，渲染准确的新应用 Chart，检查完整 manifest 差异及解析后的 ConfigMap 正文。
5. 按授权升级单个服务的 sim 环境，并确认产生新 Pod。当前为 subPath 挂载，旧 Pod 不会接收 ConfigMap 更新。本地库的 checksum 仅取 fileContent，模板改变而输入不变时 checksum 可能不变，不能假定仅升级 Chart 就会自动滚动。
6. 验收 Deployment 2/2 Ready、滚动完成、新 Pod 无 unknown directive 报错，观察重启次数并检查业务可访问性。上述集群验收尚未执行。

参考：[Helm 模板函数](https://helm.sh/docs/chart_template_guide/function_list/)、[Kubernetes ConfigMap 的 subPath 更新限制](https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/#mounted-configmaps-are-updated-automatically)。

## 复盘修正：优先检查部署源与精确 Chart

用户指出上一轮没有完整执行源配置与 Chart 的核对路径。这个遗漏成立：上一轮读取了 values-desc，但未读取 envs-desc，也未确认精确 Chart 版本的定义或模板，将“缺少实时 ConfigMap 读取工具”过早列为主要阻碍。

本轮补查结果：

- query-service-config-context(includeChart=true) 从 helm-release 确认 lucy-chart-release/magic-app:2.2.7，与平台部署记录一致。
- query-service-config 同一 revision 读取 values-desc、envs-desc；前者定义文件正文，后者声明其变量上下文，均不能在尚未执行变量解析时直接冒充最终 values。
- query-chart-deploy-context 精确核对包名 magic-app、包版本 2.2.7。schemaAvailable=true，service.configmaps[].fileContent 定义为 string；接口报告 defaultsAvailable=false，并提示包未提供根目录 values.yaml。
- 现有该工具只返回根目录默认值与字段说明，不返回 templates；check-service-config-render 只检查变量和 YAML，不执行 Helm 模板渲染。代码中的 preview-service-deploy 也仅做字段预检，不生成 manifest。

正确的优先路径为：部署对应 revision 的 values-desc/envs-desc → 按正式部署逻辑解析变量 → 精确版本完整 Chart（模板及依赖）→ 以相同 release/namespace/目标能力渲染 manifest → 检查其中 ConfigMap.data 的实际字符串。模板渲染还需核对 lookup、目标 API 能力及部署时后处理等可能影响结果的输入。

因此，追查这次非法 `|` 应先补齐模板和渲染证据，不要求首先新增独立 ConfigMap MCP。需要核对当次 Helm 部署产物时，可读取指定 revision 的 release manifest；需要排除人工修改、准入变更或旧挂载时，再读实时资源/容器文件。它们分别回答“预期生成什么”“当次记录了什么”“当前实际是什么”。

Chart 信息与两份源文档的补查成功后，后续 MCP 发现请求出现本地 publish 连接拒绝，端口检查未发现 9001 监听。本轮未自行重启用户的 IDEA 服务，未执行真实 Helm 渲染，也未绕过 MCP 调用后端 REST；不能将已读 Schema 表述为已经读过模板或完成渲染。

以下状态、日志与 409 证据仍然成立；后续建议以本节修正后的顺序为准。

## 直接故障已定位

三个 Pod 的上一轮容器日志一致报错：

```text
nginx: [emerg] unknown directive "|" in /etc/nginx/nginx.conf:3
```

Nginx 读取实际挂载的配置时，第 3 行出现无法识别的指令 `|`，进程以退出码 1 结束，导致 CrashLoopBackOff、就绪数为 0，Deployment 滚动更新超时。这个错误来自应用启动日志，不是 MCP 传输失败，也不是此前的权限 403。

`|` 从哪一环进入运行文件尚未最终定位：当前 Git 源文档的 fileContent 经 YAML 解析后并没有单独的 `|` 行。实时 ConfigMap 正文及本次运行文件尚未读取，不得把当前源配置、Chart 渲染和实时集群内容视为相同，也不能直接断言为某次源文档编辑或模板错误。

## 目标与状态

- MCP 身份：10121993。
- 服务：8824195328 / testpy-devops-api；环境：sim。
- 集群分组：lucy-mid-k8s；实际集群：gdl-dev-lucy。
- Namespace：devops-efficient-test1。
- Deployment：testpy-devops-api；期望 2、就绪 0、实际 3、updatedReplicas=1。
- 滚动策略：RollingUpdate，maxSurge=25%、maxUnavailable=25%；3 个 Pod 分属旧/新模板，不应把它误解成期望副本变为 3。
- Deployment conditions：Available=False / MinimumReplicasUnavailable；Progressing=False / ProgressDeadlineExceeded。

| Pod | Ready | 容器状态 | 上次退出 | 重启计数 |
| --- | --- | --- | --- | --- |
| testpy-devops-api-5bf64f8b86-h9k5z | false | CrashLoopBackOff | Error / 1 | 18553 |
| testpy-devops-api-5bf64f8b86-mcgth | false | CrashLoopBackOff | Error / 1 | 4788 |
| testpy-devops-api-b76496bc5-gpt8t | false | CrashLoopBackOff | Error / 1 | 4790 |

Pod phase 均为 Running，但容器正在退避等待，不等于可使用 Shell 或业务可用。三个 Pod 的上一轮启动均在同一秒内退出；对应探针 initialDelaySeconds=10，因此本次启动失败不是该轮探针在 10 秒后杀进程。上次终止原因为 Error / 1，没有 OOMKilled 证据，日志明确指向配置语法失败。

实际运行镜像为 harbor.gtjaqh.net/infra/nginx:mainline。没有覆盖 command/args，未发现自定义启动命令导致此次报错的证据。本文未尝试拉取镜像。

## 配置来源与待核对环节

Pod volumes / volumeMounts 显示：

- ConfigMap：testpy-devops-api-nginx-cfg-cm。
- subPath：nginx.conf。
- 挂载位置：/etc/nginx/nginx.conf。
- 只读挂载：true。

服务模式为 magic-v2。通过 query-service-config-context 确认类型后，读取 sim 的 values-desc。仓库 revision 为 4512a44dac2f4bab0a164632cd3c2989dfe1939d，文档 lastCommitId 为 1833102a575e7656155436142a1abd69c0dd9cc1。

有关字段为 service.configmaps[0]：name=nginx-cfg、fileType=raw、containerPath=/etc/nginx、containerFileName=nginx.conf。YAML 解析后的 fileContent 是字符串，开头为：

```nginx
events {
  worker_connections 1024;
}
http {
```

源 YAML 的 `fileContent: |` 是合法的多行字符串语法，不应仅因日志出现 `|` 就删掉这个标记。需要确认实际配置正文中为何出现该字符，继续比对实时 ConfigMap、部署时渲染内容、运行挂载及其对应版本。subPath 挂载也应纳入配置更新时间的核对。

query-service-current-config 缓存入口本次未给出 values 正文，不能以此证明无配置；本轮有效源文档证据来自 query-service-config。早期使用 values 类型读取 magic-v2 被明确拒绝为类型不匹配，随后按返回的 values-desc 类型成功读取；这不是应用故障。

## 实际 MCP 调用与能力

所有集群观察和容器请求均经真实 publish `/pubops/mcp` initialize、tools/list、tools/call 完成。没有使用本地 kubectl、直接 Kubernetes REST 或下层私有 HTTP 作为失败后的替代路径。

| 工具或能力 | 本轮结果 | 限制 |
| --- | --- | --- |
| query-service-deploy-status | 成功定位集群/Namespace，读取 Deployment/Pod YAML 和事件 | 原始 YAML 需结构化解析才能得到容器退避原因、重启次数等 |
| query-service-diagnostics | 成功返回就绪异常和 BackOff 事件 | 摘要未直接给出容器 waiting.reason、restartCount、lastState；提示人工粘贴日志，与已有日志工具未自动串联 |
| k8sctl-get-service-logs，previous=true | 三个 Pod 均成功返回，truncated=false | 足以定位这次 Nginx 启动失败，即使当前容器已崩溃 |
| query-service-config-context / config / history | 成功确认模式、读取源文档和历史版本 | 源配置不代表实时 ConfigMap 或已部署渲染结果 |
| k8sctl-exec-service-command | 只读尝试读取 nginx.conf，返回 [conflict] HTTP 409 | 容器处于 waiting，未进入可执行状态；本次未读到运行文件，不能把 409 当成成功执行 |
| Chart 模板 / Helm manifest | 补查确认精确 Chart 2.2.7 与字段定义，但未取得模板或渲染结果 | 先补齐源配置到 manifest 的证据，不能把 Schema 查询当成 Helm 渲染 |
| 实时 ConfigMap 读取 | 当前 publish 工具发现结果未提供对应能力 | 用于按需核对运行差异；不是追查源配置和模板的前置要求 |

日志举例分别来自：h9k5z 的 11:10:34、mcgth 的 11:12:43、gpt8t 的 11:11:34（均为本地时间），均含上述同一 emerg 错误。

## 后续建议

1. 优先复用平台源配置解析、精确 Chart 查询和 Helm 渲染能力，检查生成 manifest 内 ConfigMap.data；按需补充模板/渲染结果的只读 MCP，不把单独新增 ConfigMap 工具作为前置条件。
2. 若需要确认实际发布版本，读取对应 revision 的 Helm release manifest；出现预期与运行差异时，再通过服务范围约束下的实时资源读取继续核对。
3. 诊断摘要补充 waiting/terminated reason、exitCode、restartCount、Deployment conditions，并指向现有日志工具，支持按异常 Pod 读取 previous 日志。
4. 对容器未运行的 409 展示稳定且明确的安全原因，避免只显示“下游调用失败”。修复时按正式发布流程纠正配置并回读就绪状态，不能以重启代替配置修复。

## 复查入口与本轮边界

本地私有复查脚本 `.local/platform-integration/diagnosis-8824195328-2026-11-14/check-health.py` 会通过 MCP 查询实际状态，对就绪副本不足或 Pod 未就绪返回退出码 1。已执行，输出 desired=2、ready=0、unhealthy=true，三个 waiting.reason 均为 CrashLoopBackOff。该信号确认故障仍存在；本轮不修复，因此没有宣称检查已转绿。

原始响应仅保存在 Git 忽略目录，文件权限 0600；未将 kubeconfig、环境变量值或完整配置正文复制到报告。本轮没有修改业务代码，因此未运行与本次只读诊断无关的编译或单元测试，也未提交 Git。
