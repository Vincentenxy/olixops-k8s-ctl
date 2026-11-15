# k8s-ctl HTTP / MCP 逐工具真实验收矩阵

日期：2026-11-14。测试服务：`2963078505`，环境：`sim`，用户：`10121993`。

**操作器实际开放的 36 个工具，全部取得同一真实集群上的 HTTP 和 MCP 正常调用成功证据。其中 35 个工具的本轮已测场景通过；`list_events` 常规查询/新游标翻页通过，但旧游标错误分类存在待完善项。** 其余 16 个非端口 Catalog 工具未在当前平台操作器模式启用，5 个端口台账工具按要求跳过。不能将此结论表述为“57 个工具都已通过 publish”。

## 1. 调用路径和证据范围

| 标识 | 本轮使用的真实路径 | 能证明什么 |
| --- | --- | --- |
| P | 客户端 → publish `/pubops/mcp` → publish 按服务/环境解析目标 → 下层 HTTP → Kubernetes | 上层代理端到端；当前只有 8 个 `k8sctl-*` 直接代理 |
| O | 测试客户端 → 本地操作器 `/clusters/gdl-dev-lucy/api/v1/...` → Kubernetes；参数使用 publish 的实际查询结果 | 下层 HTTP 正常工作；**不等同于有对应 publish 代理** |
| M | 测试客户端 → 同一操作器 `/clusters/gdl-dev-lucy/api/v1/mcp` → 同一业务层 → Kubernetes | 下层原名 MCP 正常工作；不是 publish 的 serviceId 工具 |
| S | 原独立服务 `127.0.0.1:8091` | 另一套配置/集群，不纳入本轮目标集群通过统计 |

publish 重新发现工具共 **54 个**，其中 `k8sctl-*` 直接代理 **8 个**。操作器本轮 `tools/list` 为 **36 个**，Catalog=false；源码/独立完整模式的总清单为 **57 个**。工具总数不同是入口能力范围不同。

通过真实 publish `query-service-deploy-status(serviceId=2963078505, curEnv=sim)` 确认：

| 项目 | 实际值 |
| --- | --- |
| publish 源码 | `/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api`，使用正在运行的 IDEA 项目 |
| clusterId | `gdl-dev-lucy`，集群组 `lucy-mid-k8s` |
| Kubernetes | `v1.33.7-3` |
| namespace | `setting-efficient` |
| Deployment / container | `setting-efficient-api` |
| 业务 Pod | `setting-efficient-api-6cdb858d56-qpbhg` |
| Chart | `lucy-chart-release/magic-app:2.2.8` |

操作器重启后由 **publish 自动从数据库读取密文、通过 EnvVault 解密并重新注册目标运行实例**。测试未读取或导出 kubeconfig 原文。O/M 测试客户端使用本地受信签名文件，在内存中按 publish `CtlTokenIssuer` 的同一约定签发绑定 `k8s-ctl:gdl-dev-lucy`、用户 `10121993` 的短期 JWT；不使用任意身份 Header 覆盖身份。它验证下层认证与执行，并不替代上层服务权限验收。

原 8091 MCP 查询显示 `v1.28.0`；与 publish 的目标集群不同，因此没有用 8091 的资源写入结果冒充本轮通过。仅顺带执行的独立模板/实例只读查询也不计入平台链路通过数。

## 2. 57 工具完整表格

`PASS` 表示有真实调用及所列场景断言；`BLOCKED` 表示当前链路未启用而未执行；`SKIP` 表示按要求不测。P 历史证据单独标注日期；O/M 为本轮新增测试。

| tool name | mcp call | http call | result | test scene |
| --- | --- | --- | --- | --- |
| cluster_status | PASS，M tools/call | PASS，O GET /api/v1/cluster/status；P 无直接代理 | 已测场景通过 | publish 解析的 gdl-dev-lucy；读取版本 v1.33.7-3 和 Namespace 数量 |
| list_namespaces | PASS，M tools/call | PASS，O GET /api/v1/cluster/namespaces；P 无直接代理 | 已测场景通过 | 读取 Namespace 列表；结束后确认本轮 6 个临时 Namespace 均不在列表中 |
| list_storage_classes | PASS，M tools/call | PASS，O GET /api/v1/cluster/storage-classes；P 无直接代理 | 已测场景通过 | 查询真实 StorageClass 列表 |
| list_resources | PASS，M tools/call | PASS，O POST /api/v1/cluster/resources/summary；P 无直接代理 | 已测场景通过 | namespace=setting-efficient，查询资源摘要 |
| list_gateway_resources | PASS，M tools/call | PASS，O POST /api/v1/cluster/gateways/list；P 无直接代理 | 已测场景通过 | namespace=setting-efficient，查询网关资源；不操作 Higress 端口台账 |
| get_rollout_status | PASS，M tools/call | PASS，O POST /api/v1/cluster/workloads/rollout；P 无直接代理 | 已测场景通过 | Deployment/setting-efficient-api，desired=ready=updated=1，complete=true |
| validate_manifest | PASS，M tools/call | PASS，O POST /api/v1/cluster/kubectl/validate；P 无直接代理 | 已测场景通过 | 合法 ConfigMap YAML 本地校验，valid=true，不写集群 |
| get_workload_history | PASS，M tools/call | PASS，O POST /api/v1/cluster/workloads/history；P 无直接代理 | 已测场景通过 | 查询业务 Deployment 修订历史，limit=10 |
| get_resource | PASS，M tools/call | PASS，O POST /api/v1/cluster/resources/get；P 无直接代理 | 已测场景通过 | 读取业务 Deployment 结构化详情；缺 name 返回参数错误 |
| get_resource_yaml | PASS，M tools/call | PASS，O POST /api/v1/cluster/resources/yaml；P 无直接代理 | 已测场景通过 | 读取业务 Deployment 和 Namespace 原始 YAML；未知字段拒绝；不存在资源返回 not_found |
| list_resource_objects | PASS，M tools/call | PASS，O POST /api/v1/cluster/resources/list；P 无直接代理 | 已测场景通过 | Pod 列表 limit=1；limit=201 拒绝 |
| list_events | PASS，M tools/call | PASS，O POST /api/v1/cluster/events/list；P 无直接代理 | 常规通过；旧游标错误分类待完善 | 事件 limit=5；新 continue 跨两入口翻页成功；旧 continue 返回笼统错误，见缺陷 B01 |
| get_pod_logs | PASS，M tools/call | PASS，O POST /api/v1/cluster/pods/logs；P 本轮通过 | 已测场景通过 | 业务 Pod 最近 3 行日志；另通过 publish 服务工具复验日志 |
| list_helm_releases | PASS，M tools/call | PASS，O POST /api/v1/cluster/helm/list；P 无直接代理 | 已测场景通过 | setting-efficient 的 release 列表，limit=10 |
| get_helm_status | PASS，M tools/call | PASS，O POST /api/v1/cluster/helm/status；P 无直接代理 | 已测场景通过 | 业务 release setting-efficient-api，status=deployed；不执行升级 |
| get_helm_history | PASS，M tools/call | PASS，O POST /api/v1/cluster/helm/history；P 无直接代理 | 已测场景通过 | 业务 release 修订历史，limit=10 |
| apply_manifest | PASS，M tools/call | PASS，O POST /api/v1/cluster/kubectl/apply；P 无直接代理 | 已测场景通过 | 每条入口各自在临时 Namespace 创建 ConfigMap 和 replicas=0 的 Deployment，回读验证 |
| dry_run_manifest | PASS，M tools/call | PASS，O POST /api/v1/cluster/kubectl/dry-run；P 无直接代理 | 已测场景通过 | serverSide=false，真实服务端 dry-run；回读确认 ConfigMap 尚未落地 |
| helm_upgrade | PASS，M tools/call | PASS，O POST /api/v1/cluster/helm/upgrade；P 无直接代理 | 已测场景通过 | 每条入口各安装临时 ConfigMap-only Chart，再升级 probe:first→second，回读验证；wait=false |
| scale_workload | PASS，M tools/call | PASS，O POST /api/v1/cluster/workloads/scale；P 无直接代理 | 已测场景通过 | 临时 Deployment 显式设 replicas=0 再设 1，回读 0 和 Ready=1；错误类型拒绝 |
| restart_workload | PASS，M tools/call | PASS，O POST /api/v1/cluster/workloads/restart；P 无直接代理 | 已测场景通过 | 临时 Deployment restart；回读 restartedAt 和修订号增加 |
| rollback_workload | PASS，M tools/call | PASS，O POST /api/v1/cluster/workloads/rollback；P 无直接代理 | 已测场景通过 | 临时 Deployment 回滚到显式 revision=1；回读 restart 注解已移除 |
| delete_resource | PASS，M tools/call | PASS，O POST /api/v1/cluster/resources/delete；P 无直接代理 | 已测场景通过 | 删除临时 ConfigMap；回读确认不存在 |
| helm_rollback | PASS，M tools/call | PASS，O POST /api/v1/cluster/helm/rollback；P 无直接代理 | 已测场景通过 | 临时 Helm release 回滚 revision=1；实际 ConfigMap 值 second→first |
| helm_uninstall | PASS，M tools/call | PASS，O POST /api/v1/cluster/helm/uninstall；P 无直接代理 | 已测场景通过 | 卸载临时 Helm release；确认其 ConfigMap 不存在；不卸载业务服务 |
| create_namespace | PASS，M tools/call | PASS，O POST /api/v1/cluster/namespaces/create；P 无直接代理 | 已测场景通过 | 每条入口各创建临时 Namespace；带命名时间戳，测试后清理 |
| delete_namespace | PASS，M tools/call | PASS，O POST /api/v1/cluster/namespaces/delete；P 无直接代理 | 已测场景通过 | 删除自己的临时 Namespace；最终完整列表确认消失；default 删除拒绝 |
| update_namespace_metadata | PASS，M tools/call | PASS，O POST /api/v1/cluster/namespaces/metadata；P 无直接代理 | 已测场景通过 | 临时 Namespace 设置 labels/annotations 并回读；修改 spec 请求拒绝 |
| update_configmap | PASS，M tools/call | PASS，O POST /api/v1/cluster/configmaps/update；P 无直接代理 | 已测场景通过 | 合并 test:one→two；回读 keep:original 仍保留 |
| exec_pod_command | PASS，M tools/call | PASS，O POST /api/v1/cluster/pods/exec；P 复用 11-13 通过证据 | 已测场景通过 | 业务 Pod printf 成功；12 KiB 命令通过；exit 7 返回执行失败；NUL 命令拒绝；仅随机 /tmp 文件用于传输测试 |
| open_pod_terminal | PASS，M tools/call | PASS，O POST /api/v1/cluster/pods/terminals/open；P 复用 11-13 通过证据 | 已测场景通过 | 真实 Pod /bin/sh + tty=true；会话后续可跨 HTTP/MCP 操作 |
| write_pod_terminal | PASS，M tools/call | PASS，O POST /api/v1/cluster/pods/terminals/write；P 复用 11-13 通过证据 | 已测场景通过 | 写入 printf、sleep、Ctrl+C；中断后可继续执行命令；不把终端回显误判为输出 |
| read_pod_terminal | PASS，M tools/call | PASS，O POST /api/v1/cluster/pods/terminals/read；P 复用 11-13 通过证据 | 已测场景通过 | base64 输出、nextCursor、Ctrl+C 后输出；跨用户 HTTP 读拒绝；已关闭会话不可再读 |
| close_pod_terminal | PASS，M tools/call | PASS，O POST /api/v1/cluster/pods/terminals/close；P 复用 11-13 通过证据 | 已测场景通过 | HTTP/MCP 各关闭自己的会话；随后 read 返回 not_found |
| download_pod_file | PASS，M tools/call | PASS，O POST /api/v1/cluster/pods/files/prepare-download；P 复用 11-13 通过证据 | 已测场景通过 | 准备下载并实际 POST 文件流；0 / 65,792 字节逐字节一致；同一引用再次下载 404 |
| upload_pod_file | PASS，M tools/call | PASS，O POST /api/v1/cluster/pods/files/prepare-upload；P 复用 11-13 通过证据 | 已测场景通过 | 准备上传并实际 POST 文件流；Content-Length、返回 size/SHA-256 匹配；清理随机 /tmp 文件 |
| list_service_templates | BLOCKED；M 未注册此工具 | BLOCKED；GET /api/v1/templates（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未执行此链路；8091 独立实例的只读结果不计为平台通过 |
| get_service_template | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/templates/get（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，不能按模板版本查询 |
| save_service_template | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/templates/save（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未保存新模板版本 |
| list_standard_services | BLOCKED；M 未注册此工具 | BLOCKED；GET /api/v1/services/standard（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未执行此链路 |
| deploy_standard_service | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/deploy-standard（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false；涉及标准服务和端口分配，未执行 |
| preview_service_config | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/preview-config（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未执行渲染预览 |
| list_service_instances | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/list（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未执行此链路；8091 独立实例的只读结果不计为平台通过 |
| get_service_instance | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/get（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未查询 SQLite 实例 |
| save_service_config | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/save-config（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未创建配置修订 |
| list_service_revisions | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/revisions（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未查询修订 |
| prepare_service_deployment | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/prepare-deployment（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未冻结 YAML / 分配端口 |
| deploy_service | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/deploy（当前操作器未启用） | 当前平台链路未启用，未验收 | 这是下层 Catalog deploy_service，不能用 publish deploy-service 的成功替代；当前未启用 |
| get_service_deployment | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/deployment（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未查询标准服务部署 |
| verify_service_access | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/verify-access（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未验证标准服务 LB 端口协议 |
| list_service_deployments | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/deployments（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未查询标准服务部署历史 |
| delete_service_instance | BLOCKED；M 未注册此工具 | BLOCKED；POST /api/v1/services/delete（当前操作器未启用） | 当前平台链路未启用，未验收 | 当前平台操作器 Catalog=false，未执行标准服务资源/端口回收 |
| list_gateway_ports | SKIP，未调用 | SKIP；POST /api/v1/ports/list | 按要求跳过 | 按用户要求不测试 Higress 端口台账 |
| sync_gateway_ports | SKIP，未调用 | SKIP；POST /api/v1/ports/sync | 按要求跳过 | 按用户要求不测试 Higress 端口台账 |
| reserve_gateway_port | SKIP，未调用 | SKIP；POST /api/v1/ports/reserve | 按要求跳过 | 按用户要求不测试 Higress 端口台账 |
| release_gateway_port | SKIP，未调用 | SKIP；POST /api/v1/ports/release | 按要求跳过 | 按用户要求不测试 Higress 端口台账 |
| list_gateway_port_events | SKIP，未调用 | SKIP；POST /api/v1/ports/events | 按要求跳过 | 按用户要求不测试 Higress 端口台账 |

## 3. publish 代理工具的单独核对

按先前“不重复已通过测试”的要求，保留已通过的上层代理场景证据；本轮重点增加下层双入口与边界对照。新 Pod 通过 publish 复验日志，未将历史测试包装成本轮全量重跑。

| publish tool | 对应下层工具 / HTTP | P 端到端证据 |
| --- | --- | --- |
| `k8sctl-get-service-logs` | `get_pod_logs` / `/api/v1/cluster/pods/logs` | 本轮 11-14：重启注册后日志成功，恢复配置后再验成功 |
| `k8sctl-exec-service-command` | `exec_pod_command` / `/api/v1/cluster/pods/exec` | 11-13 已通过，本轮未重复该上层成功场景；本轮 O/M 新增对照通过 |
| `k8sctl-open-service-terminal` | `open_pod_terminal` / `/api/v1/cluster/pods/terminals/open` | 11-13 已通过，本轮未重复该上层成功场景；本轮 O/M 新增对照通过 |
| `k8sctl-write-service-terminal` | `write_pod_terminal` / `/api/v1/cluster/pods/terminals/write` | 11-13 已通过，本轮未重复该上层成功场景；本轮 O/M 新增对照通过 |
| `k8sctl-read-service-terminal` | `read_pod_terminal` / `/api/v1/cluster/pods/terminals/read` | 11-13 已通过，本轮未重复该上层成功场景；本轮 O/M 新增对照通过 |
| `k8sctl-close-service-terminal` | `close_pod_terminal` / `/api/v1/cluster/pods/terminals/close` | 11-13 已通过，本轮未重复该上层成功场景；本轮 O/M 新增对照通过 |
| `k8sctl-prepare-service-file-download` | `download_pod_file` / `/api/v1/cluster/pods/files/prepare-download` | 11-13 已通过，本轮未重复该上层成功场景；本轮 O/M 新增对照通过 |
| `k8sctl-prepare-service-file-upload` | `upload_pod_file` / `/api/v1/cluster/pods/files/prepare-upload` | 11-13 已通过，本轮未重复该上层成功场景；本轮 O/M 新增对照通过 |

上层 8 项的完整历史细节见 [publish Shell/终端/文件真实链路验收](publish-shell-flow-2026-11-13.md)。此前通过 publish 做的安装、卸载、停启式重启见 [生命周期验收](publish-service-lifecycle-2963078505-2026-11-14.md)。这些 publish 原业务工具并不调用下层同名工具，不能替代本轮下层 `helm_upgrade`、`helm_uninstall` 或 `restart_workload` 的证据；下层三项已在本轮临时资源上分别测试。

## 4. 本轮场景及结果断言

| 场景 | 结果与验证方法 |
| --- | --- |
| 16 个只读工具 | 两入口均成功；15 个工具首轮返回 data 完全相同；事件 items 相同，独立请求的 opaque continue 含不同 resourceVersion，不能按字节相等比较 |
| 事件正常分页 | 新取得的 HTTP continue 可用于 MCP 下一页；两入口下一页 items 一致 |
| Namespace 创建/元数据/删除 | 两入口各建临时 Namespace，设置 labels/annotations 并读原始 YAML 核实，结束后完整 Namespace 列表确认无残留 |
| Manifest validate/dry-run/apply | validate=true；dry-run 没有创建对象；apply 后两个对象存在，Deployment replicas=0 |
| ConfigMap 合并 | 修改 test 字段后 keep:original 保留，验证合并语义 |
| scale 0 / 1 | 显式 0 被保留，再设 1 后实际 Ready=1；不是只检查 HTTP 200 |
| restart / rollback | restart 注解和修订号推进；rollback 到显式旧修订后 restart 注解移除 |
| Helm 完整写链 | 每条入口各安装只含 ConfigMap 的临时 Chart，first→second→回滚 first→卸载对象消失；wait=false 被接受 |
| exec | printf 实际输出；outcome=completed、无 timedOut/upstreamError；约 12 KiB 的命令可通过两入口，验证旧 HTTP 8 KiB 限制不再挡住合法命令 |
| exec 错误 | exit 7：HTTP 非成功（502）/MCP isError=true；NUL 命令：参数拒绝；不把工具通信成功当命令成功 |
| 持续终端 | TTY Shell 打开、输入、base64/cursor 读取、Ctrl+C、中断后继续命令、关闭；使用拆分 printf 参数的完整输出标记排除输入回显假阳性 |
| 双入口共享会话 | HTTP open 后 MCP 发送 Ctrl+C；MCP open 后 HTTP 发送 Ctrl+C，均可继续读取输出 |
| 会话归属与关闭 | 另一用户 JWT 读取被拒绝（403/404）；关闭后 read 为 not_found |
| 文件传输 | HTTP 准备+HTTP 字节流传 0 字节；MCP 准备+HTTP 字节流传 65,792 字节二进制；返回 size/SHA-256 与源相符，下载逐字节相同 |
| 文件单次引用 | 下载后的同一引用再次 POST 返回 404；不是 MCP 返回文件原文 |
| 参数边界 | 未知字段、错误类型、缺少必填、limit=201、Namespace spec 修改均在两入口拒绝 |
| 认证边界 | 错误 JWT 和错误 audience 对 HTTP capabilities 和 MCP initialize 均为 401；没有关闭身份认证 |
| Namespace 保护 | 删除 default 被拒绝，HTTP 403 / MCP 工具错误，没有实际删除 |
| 请求 ID | 本轮记录的所有 O 响应均含 X-Request-Id |

写入目标是本轮独立临时 Namespace。有效完整写场景为 `k8sctl-http-1114-153641`、`k8sctl-mcp-1114-153641`；测试脚本准备阶段另外创建并即时清理了 4 个 Namespace，最终一并确认无残留。临时 Deployment 固定到已有业务 Pod 所在节点，使用相同现有镜像且 `imagePullPolicy: Never`，没有拉取镜像或在容器安装工具。临时 Helm Chart 仅含 ConfigMap，不含容器、PVC 或网关资源；本地临时 Chart 仓库配置已移除。

## 5. 发现的问题与未覆盖范围

### B01：旧事件分页游标错误不够准确

2026-11-14 15:41:55，将 15:35 左右得到的事件 continue 再次传入，两入口都返回 `upstream_unavailable`；HTTP 是 503，MCP 是 isError=true。随后重新取得第一页和新 continue，两个入口立即翻页成功。说明正常事件查询和分页可用，旧游标场景未给出有用的原因。

这符合分页快照/游标过期的现象，但当前响应没有暴露上游具体错误，因此不将“已证实 Kubernetes ResourceExpired”当成事实。代码 `internal/application/api/errors.go` 的分类分支也尚无 ResourceExpired/Gone 的专门映射。建议识别这类错误，返回稳定原因并提示从第一页重查；先补错误契约测试，再修复分类。已记录到 [开发待办](../roadmap.md)。当前不建议对原旧游标盲目自动重试。

本轮范围还有以下限制：

- P 的直接代理只有 8 个；其余 28 个操作器工具的 O/M 已通过，不代表用户已经能从 publish 调用它们。若产品需要，应在上层确认适配范围；优先复用已有服务查询、发布、扩缩容业务。
- `cmd/operator` 明确拒绝启用 Catalog，不能只改一行 Catalog 配置就假定平台链路接通。16 个非端口 Catalog 工具没有在当前目标链路验收；没有为了测试而对另一集群部署标准 Redis/PG 或修改其 SQLite 数据。
- 5 个 Higress 端口台账工具按要求未测试。网关资源只读查询不属于端口台账写操作。
- 没有在真实集群人为制造 Pod UID 替换、进程崩溃、引用自然过期、大文件/高并发/网关中断等故障。本轮真实测试与现有自动化测试分开统计，不能用模拟结果替代未做的环境验收。
- 下层目标资格与 Shell 拦截器仍保持此前联调的临时放行设置；本次没有完成服务级授权验收。JWT、用户归属、Pod UID 和文件完整性约束仍在执行，不能据此声称权限体系已完整验收。

## 6. 自动化检查、复现入口与最终状态

本轮执行：

```sh
go test ./...
go vet ./...
go test -race ./internal/interfaces/operator ./internal/application/shell ./tests \
  -run 'TestRegistry|TestTerminalAndBinaryDownloadThroughMainMCP|TestUploadThroughMainMCP|TestUploadFailureUnblocksStalledBody|TestOperatorHTTPWithoutMainMCP|TestOperatorProxyDownloadCancellation|TestManager|TestSession' \
  -count=1
```

上述三项最终均通过。race 实际覆盖操作器注册/隔离、会话归属与游标、主 MCP 终端/文件、HTTP-only 操作器、上传失败解除阻塞及下载取消场景；其他单元测试包含在完整 go test 中。真实集群正常链路及 B01 另由客户端调用验证。

以下为调用格式示例，需通过现有可信签发流程取得短期凭证，示例不包含真实 Token。参数来自 publish 的 status 查询，不能将示例目标当作通用默认目标：

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"query-service-deploy-status","arguments":{"serviceId":"2963078505","curEnv":"sim"}}}
```

```sh
# O：发布平台查到目标后，调用操作器 HTTP（独立测试客户端）
curl --fail-with-body "$OPERATOR/clusters/gdl-dev-lucy/api/v1/cluster/resources/yaml" \
  -H "Authorization: Bearer $SHORT_LIVED_JWT" -H 'Content-Type: application/json' \
  --data '{"namespace":"setting-efficient","kind":"Deployment","name":"setting-efficient-api"}'
```

```json
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_resource_yaml","arguments":{"namespace":"setting-efficient","kind":"Deployment","name":"setting-efficient-api"}}}
```

M 请求发往同一运行实例的 `/clusters/gdl-dev-lucy/api/v1/mcp`，先执行 MCP initialize 并携带返回的 session header。本轮已验证工具保持原名；上层按服务定位的工具仍是 `k8sctl-*`。详见 [HTTP 操作器说明](../http-operator-guide.md) 和 [OpenAPI](../http-openapi.json)。

业务服务最终仍为 1/1 Ready，restartCount=0；Deployment spec、Deployment UID、Pod UID 与本轮开始时完全一致。临时 Namespace、其内部资源、临时 Helm release 和 /tmp 测试文件均已清理，会话已关闭。未修改业务 Chart、源 values/envs、现有业务卷或端口台账。

为了验收，曾在 Git 忽略的 `.local/platform-integration/config.yaml` 临时开启操作器 MCP 和 MCP 写工具；**结束后恢复原 HTTP-only 配置并重启，随后通过 publish 日志工具再次成功调用**。原 8091 独立 MCP 配置没有修改。表格记录的是验收期间的 M 结果，不表示当前 19091 已长期开放 MCP。

完整逐工具机器可读表见 [CSV](k8sctl-http-mcp-tool-matrix-2026-11-14.csv)。原始响应、Schema 快照、断言和自动化日志保存在本机 Git 忽略目录 `.local/platform-integration/dual-entry-2026-11-14/`，权限限制为本机用户读取，不提交凭据、原始业务日志或 kubeconfig。仓库仅新增本报告/CSV 和文档索引/待办，未自动提交或推送，其他已有工作区修改保留。
