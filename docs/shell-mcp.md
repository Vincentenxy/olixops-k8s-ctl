# 主 MCP：单次容器 Shell 命令

> 2026-11-06 双入口更新：主 MCP 现有业务工具均有认证 HTTP 对应入口，终端及文件准备接口已补齐；关闭主 MCP 后仍可作为 HTTP 操作器使用。共享契约、最新开关和调用示例见 [HTTP 操作器指南](http-operator-guide.md)，上层多集群路由仍待实现。

本次实现计划的第一步：主 MCP 的 `exec_pod_command` 接受 `action: shell` 和任意命令文本，使用现有 Kubernetes MCP Server v0.1.2 的 pods_exec 执行。不新增对外 MCP 服务，不增加环境变量、内存等专用排查工具。原有固定动作暂保留以兼容现有调用。

第二阶段已加入持续终端会话与文件下载，工具和调用方式见下方章节；断开或发送 Ctrl+C 仍不等于保证远程进程终止。

## 开启方式

修改实际加载的 `configs/config.yaml`，或集群 ConfigMap 中的配置；重新编译并重启服务。配置按同名块合并，不要添加重复 YAML 键：

```yaml
shell:
  enabled: true
  interceptor:
    enabled: true
    provider: allow-all
mcp:
  enabled: true
  write-enabled: true
  diagnostics-only: false
```

还需开启 `auth.enabled`、`kubernetes.write-enabled`、`pod-exec.enabled`，配置明确的 `pod-exec.allowed-namespaces`，并准备上游执行组件。这里的功能开关默认关闭；allow-all 是内容策略占位实现，尚无危险命令保护。

本地使用 `pod-exec.mode: managed` 和官方 v0.1.2 二进制；集群使用 `mode: sidecar` 和 in-cluster，同原有 [桥接部署](kubernetes-mcp-integration.md)。PAT 与 [认证接入说明](authentication.md) 相同。客户端 URL 不变：本地 `http://127.0.0.1:8091/api/v1/mcp`，Higress 下为 `/k8sctl/api/v1/mcp`。重新连接即可发现更新后的参数。

这是完整管理 MCP，不应继续使用 `diagnostics-only: true`。该组合在启动时拒绝。完整管理工具的通用资源级授权仍有待实现，不能把原诊断专用模式的用户隔离承诺套用到全部管理工具。

已有 grants 非空时，仍要求用户精确匹配目标及 `actions: [shell]`；Pod UID 可按原有机制核对。没有 grants 时仍要求 Pod 标签 `k8s-ctl/exec-enabled: "true"`。认证、Namespace 范围、目标安全检查、写开关、Authorizer 与 Kubernetes RBAC 均保留。空命令、含 NUL 或超过 64 KiB 的命令拒绝。

## 调用

工具名继续为 `exec_pod_command`，arguments：

```json
{
  "namespace": "env-vault",
  "pod": "REPLACE_WITH_CURRENT_POD_NAME",
  "container": "env-vault-web",
  "action": "shell",
  "command": "id; pwd; printf 'hello from MCP\\n'",
  "timeoutSeconds": 20
}
```

`command` 是字符串，只供 shell 动作使用。它原样作为 `/bin/sh -c` 的一个参数传给容器，不在控制服务宿主机执行；目标容器必须有 `/bin/sh`。支持管道、重定向及多条命令，执行身份是目标容器的运行身份，没有切换用户或 sudo 参数。任意命令意味着可以修改或删除容器内数据，具体能力受运行身份、挂载和容器安全上下文约束；`read_file` 的路径白名单不限制 Shell 内的文件访问。

也可使用现有 REST `POST /api/v1/cluster/pods/exec`，同样的 JSON、Bearer 认证与拦截链路。

输出沿用 `output/upstreamError/truncated/timedOut/outcome/podUID/durationMs`，默认超时 10 秒，最大 30 秒，输出最多 256 KiB。输出不自动脱敏；上游不提供独立 stdout/stderr 或机器可读退出码。超时后的远程进程可能继续运行，结果未知时不要自动重试。

## 拦截扩展点

集中目录为 `internal/application/shell/interceptor/`，接口接收可信用户、Namespace、Pod/UID、容器和原始命令。主执行用例统一在目标检查后、上游调用前检查一次，REST 与 MCP 共用。当前 AllowAll 始终返回允许，后续可添加规则实现并扩展 provider 工厂。

- `interceptor.enabled: true`：调用配置策略；当前仅支持 allow-all。
- `interceptor.enabled: false`：跳过内容策略，其他认证和目标约束继续生效。
- 策略拒绝：不调用上游，返回 `SHELL_COMMAND_BLOCKED`（策略可提供明确的原因码及安全说明）。
- 策略异常或缺失实现：失败关闭，返回 `SHELL_POLICY_ERROR`，不透传底层错误。
- MCP 返回 `isError: true` 和原因文本；REST 返回 HTTP 403 及统一 code/msg/data，其中 data.reason 为原因码。

命令文本与完整结果不写入执行审计日志，避免把命令中的凭据记入日志。未来策略返回的消息也不得包含敏感信息。

## 测试范围

自动化测试覆盖认证后的主 MCP 工具调用、参数原样传给模拟上游、显式开关、拦截放行/拒绝/异常/关闭，以及拒绝后上游调用数不增加。没有对真实业务容器执行任意修改命令，也没有声称完成交互终端验收。

## 持续终端与文件下载（第二阶段）

主 MCP 在 `shell.enabled: true`、`pod-exec.enabled: true` 和 MCP 写开关开启时，增加以下工具。无需新增 MCP 连接或公开 sidecar 地址。已有固定诊断工具不增加；诊断专用模式不会注册这些工具。

| 工具 | 输入 | 行为 |
| --- | --- | --- |
| open_pod_terminal | namespace、pod、container、tty（可选） | 打开 `/bin/sh`，返回 sessionId；open 表示流已开始建立，需读取状态确认结果 |
| write_pod_terminal | sessionId、input | 发送原始输入，最多 16 KiB；执行命令通常需要末尾换行 |
| read_pod_terminal | sessionId、cursor、waitSeconds（0–5） | 返回最多 64 KiB 的 outputBase64、nextCursor、lostBytes 和状态 |
| close_pod_terminal | sessionId | 关闭连接并释放会话；不保证远程进程已终止 |
| upload_pod_file | namespace、pod、container、path | 准备单文件上传，返回 uploadId、uploadUrl、method、path、expiresAt、podUID；HTTP 接收原始文件流 |
| download_pod_file | namespace、pod、container、path | 准备单文件下载，返回 downloadId、downloadUrl、method、name、expiresAt、podUID；文件由 HTTP 流式传输 |

终端保留连续进程，因此 `cd`、环境变量修改等状态可跨输入保留。TTY 模式合并输出并保留 ANSI 控制序列；非 TTY 模式同样将 stdout/stderr 汇入有界输出缓冲。当前没有窗口大小调整工具，不承诺完整终端模拟器显示效果。

read 的 cursor 是字节偏移，第一次为 0，之后使用 nextCursor。输出环形缓冲最多 256 KiB；消费者过慢时旧数据丢弃，lostBytes 明确标识缺失。内容使用 Base64 保留原始字节，客户端解码后显示或保存。读到 `exited` 且 nextCursor 无新增时表示本次流正常结束；failed/disconnected/expired 不代表远程进程已确认退出。

例如依次调用：

```json
{"namespace":"env-vault","pod":"REPLACE_WITH_CURRENT_POD_NAME","container":"env-vault-web","tty":true}
```

```json
{"sessionId":"上一步返回的ID","input":"cd /tmp\npwd\n"}
```

```json
{"sessionId":"上一步返回的ID","cursor":0,"waitSeconds":2}
```

在 tty=true 时发送 `"input":"\u0003"` 表示向 PTY 发送 Ctrl+C，不是“所有远程子进程一定停止”的保证。结束使用后主动调用 close_pod_terminal。会话创建后独立于单次 MCP HTTP 请求存在，PAT 每次调用仍需有效；PAT 撤销不会立即杀掉已开的远程进程，后续客户端调用会被认证拒绝，会话由超时清理。

### 权限、拦截与生命周期

每次操作核对可信用户，会话 ID 不作为认证凭据。输入和输出读取都会重新检查原 Pod UID、目标范围、授权与 RBAC，目标替换或检查失败时关闭本地会话。关闭操作在用户归属和拦截检查后允许清理，即使目标 Pod 已不存在。拦截上下文增加 Operation 与 SessionID：terminal.open/write/read/close、file.prepare/file.download、file.upload.prepare/file.upload/file.upload.commit/file.upload.cleanup；任意终端输入都经过集中策略扩展点。当前 allow-all 没有命令风险判断。

grants 非空时，终端需要目标的 `shell` 动作；下载额外需要 `download_file` 动作，该动作表示允许下载目标内符合下载条件的路径，不使用 read_file 的逐文件白名单。本地配置已为测试身份增加该动作。

```yaml
shell:
  enabled: true
  interceptor:
    enabled: true
    provider: allow-all
  terminal:
    idle-seconds: 300
    lifetime-seconds: 1800
    max-sessions: 16
    max-per-user: 4
```

默认空闲 5 分钟、最长 30 分钟、总共 16 个会话、每用户 4 个；单次写入等待最多 5 秒，超时关闭连接。已结束但未关闭的会话也计入配额，直到关闭或清理。进程退出会关闭全部会话；会话只存在于内存，重启丢失，不适用于无会话路由的多副本部署。会话相关配置允许覆盖，但使用 `K8S_CTL_SHELL_*` 前缀，避免操作系统 SHELL 环境变量遮蔽配置块。

持续 stdin/TTY 与二进制文件不能复用上游 v0.1.2 的单次文本 pods_exec，因此新增的 Kubernetes stream adapter 使用当前 client-go 的 SPDY POST exec。仍由同一个 kubeconfig.Provider 生成客户端，沿用已选择的集群与凭据；目标 Namespace 需要 pods/exec create。网关/集群必须支持 SPDY exec；不提供 WebSocket 回退，也不另外升级 Kubernetes 依赖。单次文本命令继续复用上游组件。

### 文件保存到本地（HTTP 流式下载）

`download_pod_file` 沿用主认证 MCP，负责准备下载；不再返回 Base64 文件内容。智能体拿到下载信息后，在开发人员电脑上执行 curl，将 HTTP 响应直接保存到本地。开发人员只需要个人 PAT，不需要 kubeconfig、kubectl 或 SSH 权限。

例如工具参数：

```json
{"namespace":"env-vault","pod":"env-vault-2","container":"env-vault","path":"/app/env-vault"}
```

返回值示例（ID 与过期时间以实际结果为准）：

```json
{
  "downloadId": "REPLACE_WITH_RETURNED_ID",
  "downloadUrl": "http://127.0.0.1:8091/api/v1/cluster/pods/files/download",
  "method": "POST",
  "name": "env-vault",
  "expiresAt": "2026-11-06T10:05:00Z",
  "podUID": "REPLACE_WITH_CURRENT_UID"
}
```

下载引用有效期 5 分钟，只能由同一可信 UserID 使用，必须携带有效 Bearer PAT/JWT。下载开始时重新检查目标范围、grants、Pod UID、RBAC 和独立拦截器；成功开始一次后引用即消费，失败后需要重新调用 MCP 准备。引用只在本实例内存中，重启后失效，多副本需要后续路由方案。引用本身不是免认证的临时公开链接。

把工具返回的 URL、ID 代入下面命令。`K8S_CTL_PAT` 由本机安全注入；示例不会将真实 PAT 写进仓库。`--fail` 使 HTTP 错误返回非零退出码，不把错误 JSON 当成文件；下载中断也不会执行最后的重命名。失败时可能留下 `.part` 文件，需要删除并重新下载。

```bash
umask 077
curl --fail --show-error \
  --request POST \
  --header "Authorization: Bearer ${K8S_CTL_PAT}" \
  --header 'Content-Type: application/json' \
  --data '{"downloadId":"REPLACE_WITH_RETURNED_ID"}' \
  --output ./env-vault-linux.bin.part \
  'http://127.0.0.1:8091/api/v1/cluster/pods/files/download' \
  && mv ./env-vault-linux.bin.part ./env-vault-linux.bin
```

不要给 curl 加 `--location` 把凭据转发到其他地址，也不要使用 `--continue-at` / Range；本版不支持重定向和断点续传。Range 请求返回 400；下载成功是 `application/octet-stream` 原始文件流，区别于普通 API 的 JSON envelope，开始传输前的错误仍用统一 JSON 结构。开始传输后的错误中断 HTTP 流，不在二进制文件末尾追加错误 JSON。

服务端使用同一个 kubeconfig.Provider 的 client-go SPDY exec，以参数数组运行 `tar -chf - -C <父目录> ./<文件名>`，从 tar 流提取单个普通文件后原样输出。目标容器必须已有 tar；不依赖 Perl，不安装工具、不拉镜像。支持 ConfigMap 等符号链接所指向的普通文件；不支持目录下载、设备、FIFO 等特殊文件，仍拒绝现有保留系统路径。跟随符号链接不提供严格的路径沙箱，未来归属授权和路径策略在权限阶段统一设计。

本版**不设置固定文件大小上限**，也没有下载总时长硬编码；从容器到 HTTP 到本地按流处理，不把完整文件放进 MCP 或服务内存。客户端断开会取消本地 exec 上下文；不承诺集群断连时远端进程一定立即退出。网关、集群网络和客户端超时仍可能中断下载。文件读取期间可能被业务进程修改，不承诺一致性快照或远端哈希校验。

对外经过 Higress `/k8sctl` 时，在配置文件中填写实际外部地址：

```yaml
server:
  public-url: https://efficient-lab.qiuer.net/k8sctl
```

返回下载 URL 将包含此前缀，Higress 沿用已有前缀重写规则。服务不信任请求 Host / Forwarded 自动拼接外部地址。未设置 public-url 时，回环监听自动生成本地地址；其他监听返回相对路径，由调用方按 MCP 所在服务和网关前缀解析。示例客户端要求下载地址与 MCP 同源，避免 PAT 被发给其他站点。

也可以使用仓库的辅助客户端，它先调用主 MCP 准备，再调用 HTTP 下载，使用 `.part` 临时文件，完整成功后以 0600 权限保存，不覆盖已有文件，并输出本地 SHA-256：

```bash
go run ./cmd/mcp-download \
  --url http://127.0.0.1:8091/api/v1/mcp \
  --namespace env-vault \
  --pod env-vault-2 \
  --container env-vault \
  --path /app/env-vault \
  --out /tmp/env-vault-linux.bin
```

该命令从 K8S_CTL_PAT 读取 PAT，可通过 --token-env 指定其他变量名；仅远程 HTTPS / 本地回环 HTTP，拒绝重定向。它计算的是本地文件哈希，尚未提供与远端快照绑定的完整性校验。并发治理、大文件专项优化、断点续传等第二阶段事项见 [开发待办](roadmap.md)。


### 本地文件上传到容器

新增主 MCP 工具 `upload_pod_file`，输入为 Namespace、Pod、容器和**容器中的最终绝对路径**。工具只准备引用，不读取开发人员电脑的文件。随后由本地智能体使用 curl 读取本地文件，发送到认证 HTTP 接口。文件内容不进入 MCP 或模型上下文。

```json
{"namespace":"env-vault","pod":"env-vault-2","container":"env-vault","path":"/tmp/local-config.yaml"}
```

返回 uploadId、uploadUrl、method（POST）、path、expiresAt、podUID。uploadId 与下载引用分开管理，有效期 5 分钟，绑定认证 UserID 与 Pod UID，一次使用；重启失效、多副本尚需路由。准备和上传均验证认证、范围、Pod UID、RBAC 与拦截器。grants 非空时需要独立的 `upload_file` 动作，`download_file` 不授予上传；本地配置仅为现有 env-vault-2 测试目标补充此动作，集群配置需明确添加。该 Pod 当前根文件系统与 /tmp 只读，授权上传不代表文件系统可写；需要选择已有可写挂载作为目标。

```bash
curl --fail --show-error \
  --request POST \
  --header "Authorization: Bearer ${K8S_CTL_PAT}" \
  --header 'Content-Type: application/octet-stream' \
  --header 'X-Upload-Id: REPLACE_WITH_RETURNED_ID' \
  --data-binary @./local-config.yaml \
  'http://127.0.0.1:8091/api/v1/cluster/pods/files/upload'
```

上传接口为 `POST /api/v1/cluster/pods/files/upload`。URL 使用工具返回值，经过 Higress 时含 `/k8sctl` 前缀；与下载复用 `server.public-url`。本地文件名不传给服务端，目标路径只能来自已认证 MCP 准备阶段，不能用 HTTP header 临时换目标。成功响应沿用统一 JSON envelope：

```json
{"code":0,"msg":"success","data":{"path":"/tmp/local-config.yaml","size":123,"sha256":"...","podUID":"..."}}
```

size 是实际接收的字节数，sha256 是服务端传输流的哈希。发布前会在容器中核对临时文件大小，并再次读取临时文件计算 SHA-256，与传输流比较；任一不一致均拒绝发布。可将返回哈希与本地文件比较，但不承诺 fsync 持久性或其他进程并发修改后的内容。出现业务错误时返回非 2xx 状态及同一 JSON 结构，拦截器拒绝返回 403 与 reason。提交期间断连可能导致最终文件已创建但响应未到达，结果未确认时先查询目标，不能盲目重试。

首版上传规则：

- 只上传单个普通文件，**只创建新文件，不覆盖已有文件、目录或符号链接**；不自动创建父目录。修改已存在的配置需要单独受控的后续替换流程，上传后也不自动重启、reload 或执行文件。
- 先通过不带 stdin 的 exec 在目标父目录建立随机临时目录及同名临时文件，确认可写后，容器内固定脚本按 Content-Length 接收 stdin 写入临时文件，保留 stdin 直到远端确认完成，避免提前关闭导致尾部丢失；传输完整、远端大小和哈希一致，且重新校验 Pod 与拦截器后，用 `ln` 原子创建最终目录项。目标已存在则失败，不改原内容。使用同一文件系统的硬链接，文件系统不支持时返回失败。
- 新文件权限 0600，所属用户为容器内执行命令的用户。遵守目标文件系统的写权限和只读挂载约束，ConfigMap/Secret 投射卷不能通过上传修改。
- 需要目标已有 `/bin/sh`、支持 `-c` 的 `head`、`wc`、`sha256sum`、`mkdir`、`rmdir`、`rm` 与 `ln`，不安装程序、不拉镜像。命令固定，目标路径通过独立 argv 传入，不拼接进脚本。
- 不设置固定大小上限，不在服务主机缓存整个文件。客户端使用 curl `--data-binary`，必须携带准确 Content-Length（curl --data-binary @文件会自动设置）；未知长度的 chunked 上传不支持。服务端不接收 multipart、压缩内容或 Content-Range/Range。上传读取失败不会主动发布最终文件。
- 失败和成功后都尽力清理临时文件；Pod 不可达、替换、权限撤销或进程崩溃时可能遗留 `.k8s-ctl-upload-*` 临时目录，不承诺全故障场景清理。父目录符号链接、Pod 检查与 exec 间竞态也尚未提供严格沙箱保证。

独立拦截操作为 `file.upload.prepare`、`file.upload`、`file.upload.commit`、`file.upload.cleanup`，当前仍默认放行，没有实现危险路径判断或新的资源归属权限。断点续传、覆盖策略、并发治理、GB 级验证、临时目录回收等后续事项见 [开发待办](roadmap.md)。
