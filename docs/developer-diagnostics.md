# 开发人员通过 MCP 排查 Pod

需要重启后导入客户端并手动验收时，按 [MCP 接入与手动测试](mcp-manual-testing.md) 操作；本文说明能力设计与约束。

本功能面向没有 Kubernetes 凭据的开发人员。开发人员只配置 k8s-ctl MCP 地址和个人 PAT（或公司 JWT），由服务端持有 Namespace 范围的 Kubernetes 凭据。配置、环境变量、内存和日志采集均通过 MCP；客户端不调用 kubectl、SSH 或 Kubernetes API。

## 两个工具与调用方式

开发人员专用实例设置 `mcp.diagnostics-only: true`，只注册：

- `get_pod_logs`：复用已有有界日志读取。
- `exec_pod_command`：通过 Kubernetes MCP Server v0.1.2 执行固定诊断动作。

此模式不注册部署、删除、扩缩容、Helm、目录等管理 MCP 工具，也不注册对应管理 REST 路由。目录初始化和沙箱过期清理后台任务也被停用，即使误保留 catalog.enabled=true，诊断实例也不会进行目录后台回收。健康接口、认证/个人令牌接口和日志/exec REST 入口继续按其认证规则提供。

```json
{"namespace":"env-vault","pod":"env-vault-web-5dcdc4c59-7wt8t","container":"env-vault-web","action":"environment","timeoutSeconds":20}
```

| action | 用途 | 参数 |
| --- | --- | --- |
| environment | 读取容器实际环境变量 | 无附加参数 |
| memory_usage | 读取 cgroup 内存指标，支持 v2/v1 回退 | 无附加参数 |
| process_list | 枚举 /proc；其中数字项为 PID，无需 ps | 无附加参数 |
| process_status | 读取某个进程的 /proc/PID/status | pid，默认 1，范围 1–4194304 |
| read_file | 读取明确获准的配置文本，最多 64 KiB | path，必须与管理员配置逐字匹配 |
| redis_ping / nginx_check / disk_usage | 原有固定诊断动作 | 无附加参数 |

日志仍独立使用 get_pod_logs 的 namespace、pod、container、tailLines 等参数。工具结果通过原有 text/output 字段返回，客户端可将原文保存为本地文件；服务器不生成公共下载链接或保存一份共享诊断压缩包。原文不自动脱敏，不将完整结果、令牌或配置内容写入服务日志。

不接受 shell 字符串、任意 argv、解释器代码、stdin 或 TTY。本功能不是交互式 SSH 终端，也不能据此声称支持 Ctrl+C/远程进程必定终止。超时预算覆盖授权、目标检查和执行；提交执行后超时返回 unknown，不自动重试。

## 管理员配置与用户权限

完整起点为 [config.diagnostics.example.yaml](../configs/config.diagnostics.example.yaml)。部署时用它替换主容器 ConfigMap 的 config.yaml，填入真实身份和目标，并准备认证 Secret。示例中的占位值需要替换，不能直接当成可用目标。

```yaml
mcp:
  enabled: true
  write-enabled: true
  diagnostics-only: true
pod-exec:
  enabled: true
  mode: sidecar
  endpoint: http://127.0.0.1:8082/mcp
  allowed-namespaces: [env-vault]
  grants:
    - user-id: VERIFIED_COMPANY_USER_ID
      namespace: env-vault
      pod: env-vault-web-5dcdc4c59-7wt8t
      container: env-vault-web
      pod-uid: CURRENT_POD_UID
      actions: [logs, environment, process_list, process_status, memory_usage, read_file]
      files:
        - /etc/nginx/nginx.conf
        - /etc/nginx/conf.d/default.conf
        - /usr/share/nginx/html/auth-config.json
```

身份取认证后的可信 UserID，不信任 MCP 参数、HTTP 头或客户端声明的 owner。授权精确匹配 user/ns/pod/container/action；read_file 额外匹配文件路径。专用诊断模式要求配置非空 Pod UID，并在读取或执行前核对，避免同名 Pod 重建后默认继承授权。该 UID 本身代表管理员的明确 opt-in，因此无需开发人员有权限给 Pod 加标签。发生滚动更新时管理员需更新 grant 并重启实例；目前不是自动跟随 Deployment 的用户权限系统。

日志和 exec 保留 Authorizer 扩展点，但此专用模式额外强制上述 grant 检查，未获准的用户默认拒绝。在非 diagnostics-only 的管理实例中，其他工具依然遵循原有授权设计；不能仅填 grants 就把完整管理实例当成开发人员入口。

目标仍排除系统 Namespace、控制服务自身、hostNetwork/hostPID/hostIPC、hostPath 和 privileged Pod。Pod UID 核对与 Kubernetes exec/logs 建连之间仍有 API 层竞态，不能宣称强 UID precondition 或完整沙箱隔离。

## 文件安全读取与运行时要求

read_file 不通过 readlink 检查后再 head，避免两次操作之间替换符号链接。它通过上游 pods_exec 执行一段固定、服务端维护的 Perl/Fcntl 读取器：逐级持有目录描述符，以 O_NOFOLLOW 打开每个组件，并只读取普通文件。符号链接、目录、设备和 FIFO 均拒绝；/proc、/sys、/dev 和标准 ServiceAccount Secret 路径也不能配置为可读文件。文件路径只作为独立 argv，用户不能提供或修改解释器程序。

目标镜像需要已有 Perl/Fcntl 和可访问的 /proc/self/fd；没有时明确失败，不安装程序或拉取调试镜像，也不退回 readlink/head 等较弱的读取方式。environment、process_status 等动作使用目标已有的 env/cat/ls。读取配置是文本协议，不提供二进制文件传输。此约束不能防御已经控制容器运行时或内核的攻击者。

## 部署和 RBAC

生产部署仍建议同 Pod sidecar，仅监听 127.0.0.1:8082，不新增公网入口。k8s-ctl 与 sidecar 使用同一 ServiceAccount；在每个获准业务 Namespace 授予 get pods、get pods/log、get/create pods/exec，以及 Kubernetes 所需的普通 API discovery 访问。示例见 [RBAC](../deployments/components/kubernetes-mcp/rbac.example.yaml)。不向开发人员提供该 ServiceAccount token、kubeconfig 或集群级角色。

日志/exec 专用 Role 的权限是平台服务的权限上限；grants 控制具体用户可调用的目标与动作。Kubernetes RBAC 本身不能限制容器内的具体命令。

sidecar 模式继续强制 kubernetes.in-cluster=true。本地验证可明确设置 pod-exec.mode=managed、binary 指向已校验的官方 v0.1.2 二进制。主服务在启动时通过同一个 kubeconfig.Provider 固定所选 context、单集群和凭据，再给上游 stdio 进程提供 0600 临时凭据；会话结束即清理。客户端仍只用 PAT，不感知 kubeconfig。

managed 模式不连接一个可任意配置集群的外部 HTTP MCP 服务，因此不放松 sidecar 的同集群约束。它是受控的本地验证/服务器进程模式，最多四个执行会话；上游进程 stderr 不转发给用户。所选 tokenFile 会读入快照，轮换后需要重启；生产长时间运行优先使用 sidecar 的 in-cluster 凭据刷新。依赖外部认证插件时，插件所需配置应显式存在 kubeconfig 中，不能依赖任意继承的环境变量。

## 真实验证

本次使用仅持有 PAT 的 Go MCP SDK 客户端，未导入 Kubernetes 库、未执行 kubectl 或 SSH，从真实 poc-cluster 的指定 Pod 取得日志、环境变量、内存、PID 状态和三份配置。授权与未授权用户分别验证，未授权用户、其他 Pod、禁用路径、任意命令和管理入口被拒绝。详细结果见本次测试报告。未将公司真实 JWT/SSO、交互式终端、C++ heap profiler 或真实进程中断包含在该验证中。
