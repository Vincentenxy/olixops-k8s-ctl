# MCP 接入与手动测试

更新日期：2026-11-06。

> 主 MCP 的单次任意 Shell 命令已另行实现，见 [Shell MCP 使用说明](shell-mcp.md)。本文保留诊断专用模式的配置与测试；使用主 MCP Shell 时请关闭 diagnostics-only，并按新说明开启 shell。
本文用于重启当前代码后，把 MCP 添加到 Codex，手动测试日志、环境变量、配置文本和内存查询。客户端只需 MCP 地址和 PAT，不需要 kubeconfig、kubectl 或 SSH；Kubernetes 凭据由服务端持有。

## 1. 先选择本次测试的入口

| 服务模式 | MCP 地址 | 认证 | 工具范围 |
| --- | --- | --- | --- |
| 本地开发人员诊断实例（本文推荐） | `http://127.0.0.1:8091/api/v1/mcp` | Bearer PAT/JWT | 启用 exec 后只有 `get_pod_logs`、`exec_pod_command` |
| 集群开发人员诊断实例，经现有 Higress | `https://efficient-lab.qiuer.net/k8sctl/api/v1/mcp` | Bearer PAT/JWT | 同上；需已更新镜像、配置和 sidecar |
| 认证后的完整管理实例 | `/api/v1/mcp`，按部署补域名/前缀 | Bearer PAT/JWT | 按配置提供管理工具，不适合作为普通开发人员的诊断入口 |
| 旧兼容实例，`auth.enabled=false` | `/api/v1/pub/mcp`，按部署补域名/前缀 | 公开入口 | 原有管理工具；不能用于本文受控 exec 测试 |

传输方式均为 **Streamable HTTP**。不要填写健康检查 URL、`/sse` 或上游 `127.0.0.1:8082/mcp`。客户端只连接 k8s-ctl，服务器内部如何调用上游不影响客户端配置。

本文端口 8091 是配置示例，不代表现有进程已加载新代码。此前自动化测试使用的临时实例与 PAT 不作为长期接入配置，测试 PAT 已撤销，需要签发新的个人 PAT。

## 2. 管理员：准备本地服务

这部分由平台维护者完成一次，普通开发人员从第 4 节接入即可。所有服务端选项直接写 YAML，密钥通过文件提供。

### 2.1 准备签名密钥

在项目根目录操作。已有密钥请继续使用，不要为重启而重新生成，否则旧 PAT 会失效。首次创建方法见 [认证接入说明：本地准备与首个 PAT](authentication.md#本地准备与首个-pat)。确认存在：

```text
.local/auth/pat-private.pem
.local/auth/pat-public.pem
```

私钥、PAT、SQLite 数据库和 kubeconfig 都不要提交到 Git。

### 2.2 修改真正会被加载的配置

程序从当前工作目录读取 `./configs/config.yaml`，**不会自动读取** `config.diagnostics.example.yaml`。将下面配置合并到现有 `configs/config.yaml`，同名顶层配置块只保留一份；无需改动现有 kubeconfig 文件内容。

```yaml
server:
  host: 127.0.0.1
  port: 8091
  mode: debug
kubernetes:
  in-cluster: false
  kubeconfig:
    source: file
    path: ""       # 空值读取当前用户 ~/.kube/config
    context: ""    # 空值使用 current-context；也可明确指定目标 context
  allowed-namespaces: [env-vault]
  write-enabled: true
  command-timeout-seconds: 30
mcp:
  enabled: true
  write-enabled: true
  diagnostics-only: true
catalog:
  enabled: false
auth:
  enabled: true
  company:
    enabled: false
  pat:
    enabled: true
    issuer: k8s-ctl
    audience: k8s-ctl
    key-id: pat-v1
    private-key-file: .local/auth/pat-private.pem
    public-key-file: .local/auth/pat-public.pem
    database-path: ./data/auth.db
pod-exec:
  enabled: true
  mode: managed
  binary: /Users/vincent/Desktop/codes.nosync/olixops-k8s-ctl/.local/kubernetes-mcp-bin/kubernetes-mcp-server
  allowed-namespaces: [env-vault]
  grants:
    - user-id: diagnostic-developer
      namespace: env-vault
      pod: env-vault-web-5dcdc4c59-7wt8t
      container: env-vault-web
      pod-uid: REPLACE_WITH_CURRENT_POD_UID
      actions: [logs, environment, memory_usage, process_list, process_status, read_file]
      files:
        - /etc/nginx/nginx.conf
        - /etc/nginx/conf.d/default.conf
        - /usr/share/nginx/html/auth-config.json
```

必须把 `REPLACE_WITH_CURRENT_POD_UID` 改为当前目标的真实 UID；Pod 名称和容器名也需确认。管理员可通过已有管理 MCP 的资源查询工具核对；不要求开发人员取得 Kubernetes 凭据。上次测试的 Pod UID 为 `49b4db43-e010-4ad7-8f9e-1b4080b4e576`，仅用于历史核对，Pod 重建后不能继续沿用。

`diagnostic-developer` 是示例身份，PAT 的 `--user-id` 必须与 grant 完全一致；正式使用时换成核实过归属的稳定员工 ID。grant 按用户、Namespace、Pod 名称、UID、容器、动作和文件逐项匹配。这里仅授权表中的六种动作，其他固定动作也不会自动放开。

`managed` 模式在服务端启动官方 Kubernetes MCP Server **v0.1.2** 的 stdio 子进程。上面的二进制路径是本机已验证的位置，换机器需自行准备该版本并修改路径；不会自动下载或拉镜像。主服务和子进程使用同一 Provider 的单集群快照，开发人员客户端不接触该快照。

虽然这些动作以诊断为目的，Kubernetes `pods/exec` 本身属于写请求，因此两个 write-enabled 都要开启。`diagnostics-only: true` 会隐藏管理工具及管理 REST，并停用目录后台任务；不要仅开启 exec 而遗漏此开关。

### 2.3 签发本次使用的 PAT

配置完成并准备好密钥后，在项目根目录执行：

注意必须同时设置 `auth.enabled: true` 和 `auth.pat.enabled: true`，字段名是 `enabled`，不是 `enable`。只打开 PAT 子开关仍会报 `auth and PAT must be enabled`；修改的是 `--config-dir` 指定目录中的 `config.yaml`。

```bash
go run ./cmd/pat --config-dir ./configs \
  --user-id diagnostic-developer --name codex-manual-test --days 30
```

返回 JSON 中的 `token` 是本次 PAT，只返回一次，保存到个人凭据管理工具。签发命令必须访问主服务同一份配置、签名密钥和 `auth.db`，否则可能签名正确但数据库中没有记录。命令只能由管理员使用，开发人员不能靠自填 user-id 获得授权。

### 2.4 重启新程序

先正常停止原来占用 8091 的服务，再在项目根目录启动当前代码：

```bash
go run ./cmd
```

如果使用编译产物启动，需要重新编译，并保证工作目录下有 `configs/config.yaml`。主服务目前没有 `--config-dir` 参数；该参数属于上面的 PAT 管理命令。

确认启动成功：

```bash
curl --fail http://127.0.0.1:8091/api/v1/pub/health
curl --fail http://127.0.0.1:8091/api/v1/pub/ready
```

健康检查不校验 PAT。接着用个人 PAT 请求 `/api/v1/auth/me`，确认身份与 grant 的 user-id 一致；可使用客户端凭据功能发送 `Authorization: Bearer <PAT>`，不要把真实 token 写进共享文档。

## 3. 管理员：集群部署与本地的区别

如果这次重启的是集群服务，客户端改用第 1 节的 Higress 地址，并做以下替换：

- 使用 [集群诊断配置示例](../configs/config.diagnostics.example.yaml)，替换真实用户和 Pod UID，开启 JWT/PAT；密钥路径使用 `/app/secrets/auth/`，数据库使用持久卷上的 `/data/auth.db`。
- `kubernetes.in-cluster: true`、`kubernetes.kubeconfig.source: in-cluster`、`pod-exec.mode: sidecar`，endpoint 为 `http://127.0.0.1:8082/mcp`。不使用本机 managed 二进制路径。
- 主容器与上游 sidecar 在同一 Pod，使用同一 ServiceAccount。目标 Namespace 需要 `get pods`、`get pods/log`、`get/create pods/exec` 等必要 RBAC，示例见 [rbac.example.yaml](../deployments/components/kubernetes-mcp/rbac.example.yaml)。
- [deployments/catalog.yaml](../deployments/catalog.yaml) 单独部署不包含 sidecar；需使用包含组件的 [Kustomize 入口](../deployments/kustomization.yaml)，并核对其中最终生成的配置。不要只修改未被部署引用的本地 YAML。
- Higress 保留 `/k8sctl` 前缀重写规则，并转发 Authorization。认证开启后旧 `/api/v1/pub/mcp` 不再注册；健康探针路径不变。

完整部署方法见 [Kubernetes MCP Server 接入](kubernetes-mcp-integration.md)；管理员签发 PAT 的容器命令见 [认证接入说明](authentication.md)。开发人员始终不需要 ServiceAccount token 或集群管理员角色。

## 4. 开发人员：把 MCP 添加到 Codex

### 4.1 配置 URL 和 PAT

Codex 的配置文件为 `~/.codex/config.toml`，在现有文件中添加下面的独立配置块，不要覆盖其他设置：

```toml
[mcp_servers.k8s_ctl_diagnostics]
url = "http://127.0.0.1:8091/api/v1/mcp"
bearer_token_env_var = "K8S_CTL_PAT"
tool_timeout_sec = 60
```

连接集群时，只将 url 改为 `https://efficient-lab.qiuer.net/k8sctl/api/v1/mcp`。该域名是项目现有部署示例，实际地址以管理员提供为准。

也可以由 CLI 添加同一连接，二选一，不必重复添加：

```bash
codex mcp add k8s_ctl_diagnostics \
  --url http://127.0.0.1:8091/api/v1/mcp \
  --bearer-token-env-var K8S_CTL_PAT
```

`K8S_CTL_PAT` 保存 token 本身，不带 `Bearer ` 前缀。它是 **Codex 客户端读取凭据的环境变量**，不是要求用环境变量配置 k8s-ctl 服务端。将 PAT 注入实际启动 Codex 的进程环境；只在某个终端 export，不会自动更新已运行的桌面应用。

本机 zsh 下，可在启动 CLI 的终端用隐藏输入设置，避免把 token 字面量写进历史：

```zsh
read -s 'K8S_CTL_PAT?请输入个人 PAT: '
export K8S_CTL_PAT
printf '\n'
codex
```

桌面版要确保启动它的进程能读取该变量，再完全退出并重新启动应用。如果使用桌面设置页提供的 HTTP 认证头配置，则请求头应为 `Authorization: Bearer <个人PAT>`；以实际版本支持的凭据设置为准，不要把 token 作为对话消息发送给模型。

配置字段及 HTTP Bearer 方式依据 [OpenAI 官方 MCP 文档](https://learn.chatgpt.com/docs/extend/mcp)，并已核对本机 `codex mcp add --help` 支持 `--bearer-token-env-var`。本服务未实现 OAuth 登录发现，不能用 `codex mcp login` 代替 PAT 签发。

### 4.2 重新连接并检查发现结果

保存后重新加载 MCP 连接或重新打开会话；必要时重启 Codex。CLI 可通过 `codex mcp list` 查看配置，交互界面用 `/mcp` 检查连接。配置存在不等于已成功认证，还需实际调用工具。

可以直接对智能体说：

> 查看 k8s_ctl_diagnostics 这个 MCP 连接，列出它实际提供的工具。本次只使用这个连接，不使用本地 kubectl、SSH 或直接访问 Kubernetes API。

按本文配置应发现 `get_pod_logs` 和 `exec_pod_command` 两个工具。显示名称可能带客户端前缀，实际协议名保持不变。若发现几十个管理工具，说明连到了完整管理实例或 `diagnostics-only` 尚未生效。

## 5. 逐项手动测试

以下例子假设第 2 节中的目标仍然存在且 grant 已更新。滚动发布后由管理员先更新 Pod 名称、UID 与 grant 并重启服务。

### 5.1 日志

对智能体说：

> 仅使用 k8s_ctl_diagnostics 的 MCP，读取 env-vault Namespace 下 env-vault-web-5dcdc4c59-7wt8t Pod、env-vault-web 容器最近 100 行日志，保留时间戳，告诉我是否存在错误。

对应工具 `get_pod_logs`，arguments：

```json
{"namespace":"env-vault","pod":"env-vault-web-5dcdc4c59-7wt8t","container":"env-vault-web","tailLines":100,"timestamps":true}
```

返回 `text` 和 `truncated`。日志默认 200 行，最多 5000 行、256 KiB；不是持续 follow。查询上一轮容器日志可传 `previous:true`，没有历史容器时失败属于正常的目标状态反馈。

### 5.2 环境变量、内存和进程

对智能体说：

> 仅通过这个 MCP，依次读取上述容器的环境变量、cgroup 内存、进程列表和 PID 1 的状态；逐项报告实际结果，不运行自定义 shell 命令。

调用 `exec_pod_command`；每次使用以下基础参数，分别更换 action：

```json
{"namespace":"env-vault","pod":"env-vault-web-5dcdc4c59-7wt8t","container":"env-vault-web","action":"environment","timeoutSeconds":20}
```

| action | 额外参数 | 结果说明 |
| --- | --- | --- |
| `environment` | 无 | 容器内 `env` 的原始输出，不自动脱敏 |
| `memory_usage` | 无 | cgroup 内存用量、上限和统计，支持 v2/v1；不能单凭一次采样判断泄露 |
| `process_list` | 无 | `/proc` 目录项，其中数字项是 PID，不是完整 ps 输出 |
| `process_status` | `"pid":1` | `/proc/1/status`，可查看 VmRSS、VmSize、线程数等实际存在的字段 |

`timeoutSeconds` 默认 10，最大 30。`pid` 仅供 `process_status` 使用。

### 5.3 读取配置文件

对智能体说：

> 通过 exec_pod_command 的 read_file 动作，读取上述容器的 /etc/nginx/nginx.conf 和 /etc/nginx/conf.d/default.conf，保留原文。

每个文件单独调用，例如：

```json
{"namespace":"env-vault","pod":"env-vault-web-5dcdc4c59-7wt8t","container":"env-vault-web","action":"read_file","path":"/etc/nginx/nginx.conf","timeoutSeconds":20}
```

path 必须是管理员列入 grant 的精确路径，仅供 `read_file` 使用。单文件最多 64 KiB，超限返回截断标记。目标容器需要已经安装 Perl/Fcntl 且可访问 `/proc/self/fd`；不满足时失败，不安装依赖或拉取镜像。符号链接、目录、设备、FIFO 与禁用系统路径均拒绝。

### 5.4 导出到本地

> 将刚才通过 MCP 得到的日志、环境变量、内存、PID 1 状态和两份配置原文保存到当前工作区 .local/manual-mcp-test/，每项一个文件，另外写一份采集说明，注明 Namespace、Pod、容器、时间及是否截断。采集只能调用 k8s_ctl_diagnostics，允许使用本地文件工具保存返回结果。

MCP 返回内容，智能体通过本地文件能力保存；服务端没有“下载容器任意文件”或生成公共压缩包的工具。检查保存的是原始 `text`/`output`，并在说明中保留各次错误和截断状态。

## 6. 拒绝与异常测试

| 测试 | 预期 |
| --- | --- |
| 使用无 grant 的另一个有效用户 PAT | 工具拒绝；有效 PAT 不代表有目标权限 |
| 将 pod 改为未授权目标 | 工具拒绝 |
| `read_file` 改读 `/var/run/secrets/kubernetes.io/serviceaccount/token` | 拒绝，不返回内容 |
| 将 action 改为 `shell`，或提供 `command`/`argv` | 拒绝；不支持任意 shell 或命令数组 |
| `process_status` 传负数 pid | 参数校验失败 |
| 调用删除、部署等管理 MCP | 诊断实例没有该工具 |
| 不发送 PAT、发送撤销或到期 PAT | HTTP 401，不能初始化或调用工具 |

有效调用也要检查 MCP `isError` 和 exec 的 `upstreamError`、`timedOut`、`outcome`、`truncated`。HTTP 200 或 `outcome: completed` 不代表目标命令业务成功。`output` 是上游文本，不保证分别返回 stdout、stderr 或机器可读退出码。

超时或中断发生在提交执行后时，结果可能为 `unknown`，不代表容器内进程一定终止。当前没有交互式终端、stdin、TTY、远程 Ctrl+C 保证或 C++ heap profiler 工具；不要把此测试当成交互式 shell 中断验收。

## 7. 常见问题

| 现象 | 排查 |
| --- | --- |
| 重启后仍看不到 exec | 确认运行新编译产物；实际读取的是 configs/config.yaml；exec、MCP 和两个写开关均已开启 |
| 服务启动提示 in-cluster/sidecar 约束 | 本机使用 managed；集群 sidecar 使用 in-cluster，不要混用 |
| 新认证地址 401 | 确认 Codex 进程可读取 PAT；令牌未过期/撤销；与服务器使用同一签名密钥及数据库 |
| 旧公开地址 404 | 开启认证后是预期行为，改用 /api/v1/mcp，经过 Higress 加 /k8sctl |
| 只能看到几十个管理工具 | 核对 MCP URL、服务配置和 diagnostics-only；重新连接获取清单 |
| 日志和 exec 都被拒绝 | 核对 PAT 的真实 UserID、精确 grant、Pod UID、容器名、Namespace 范围和 RBAC |
| 滚动发布后原来可用现在被拒绝 | Pod 名称/UID 变化，由管理员更新授权并重启 |
| 日志正常但 exec 失败 | 检查上游 v0.1.2、managed 二进制/sidecar 可用性、pods/exec RBAC 和目标 Pod 限制 |
| 环境变量可读但文件不可读 | 核对文件是否精确授权、普通文件，以及目标是否已有 Perl/Fcntl；不临时装依赖 |
| 浏览器打开 MCP 地址报错 | 用 Streamable HTTP 客户端，浏览器 GET 不能代替 MCP 初始化 |

本轮真实 MCP 验证记录见 [开发人员诊断测试报告](testing/mcp-developer-diagnostics-2026-11-06.md)。该报告证明测试时的代码和目标行为，不代表之后重启的实例自动采用了同一份配置。
