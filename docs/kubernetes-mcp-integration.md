# Kubernetes MCP Server 接入

> 2026-11-06 双入口更新：主 MCP 现有业务工具均有认证 HTTP 对应入口，终端及文件准备接口已补齐；关闭主 MCP 后仍可作为 HTTP 操作器使用。共享契约、最新开关和调用示例见 [HTTP 操作器指南](http-operator-guide.md)，上层多集群路由仍待实现。

开发人员自助排查请优先阅读 [专用诊断入口](developer-diagnostics.md)：它只暴露两个工具，提供精确用户授权、新增配置/环境/内存动作，以及通过同一 Provider 固定集群的 managed stdio 模式。本文保留完整管理实例的 sidecar 接入说明。

本次接入复用 containers/kubernetes-mcp-server v0.1.2 的 pods_exec。现有 Kubernetes、Helm、目录和端口台账工具继续由 k8s-ctl 提供；已有 get_pod_logs 继续提供日志，不重复注册上游 pods_log、资源 CRUD 等工具，也不提供通用 tools/call 代理。

## 请求链路与版本

客户端 → Higress /k8sctl → k8s-ctl /api/v1/mcp → JWT/PAT → ExecPodCommand → 私有 sidecar /mcp → pods_exec → Kubernetes pods/exec。

上游采用独立进程，不引入第二套 Kubernetes Go 依赖。镜像固定为 quay.io/containers/kubernetes_mcp_server:v0.1.2；核对源码提交为 66553fed5c16454e5814b41e331b4357f15806d8。升级需重新验证参数、配置、返回语义及 RBAC。上游项目：[固定版本源码](https://github.com/containers/kubernetes-mcp-server/tree/v0.1.2)。

同 Pod sidecar 仅监听 127.0.0.1:8082，无 Service、Ingress 或额外公开端口。上游只启用 core 工具集的 pods_exec，固定 in-cluster 集群。k8s-ctl 使用官方 Go MCP SDK 的 Streamable HTTP 客户端，每次执行创建一次会话，不转发用户 JWT/PAT、Cookie 或身份头，不允许重定向、DNS 地址、HTTP 代理或客户端指定连接地址。适配器只允许调用 pods_exec，参数只能由应用层生成。

sidecar 与主容器共享 Pod 网络及 ServiceAccount，是同一信任边界，不能声称两者之间有独立网络认证或独立 Kubernetes 身份。启用 exec 时，启动校验强制 kubernetes.in-cluster=true，确保主容器检查与 sidecar 执行均连接本集群；sidecar 模式不支持外部 kubeconfig；managed 模式另通过同一 Provider 快照保持一致。

## 已有日志与新增执行能力

| 能力 | MCP | HTTP |
| --- | --- | --- |
| Pod 日志快照，复用原有逻辑 | get_pod_logs | POST /api/v1/cluster/pods/logs |
| 固定动作容器诊断，复用上游 pods_exec | exec_pod_command | POST /api/v1/cluster/pods/exec |

日志参数与现有工具相同：namespace、pod、container、tailLines、sinceSeconds、previous、timestamps；默认最后 200 行，最多 5000 行和 256 KiB，sinceSeconds 最多 7 天。原文返回，不自动脱敏。日志为快照，不做 follow、文件读取或持久化存储。工作负载日志聚合本次不新增。

以下是原有固定动作示例。新增 environment/read_file/process_status/memory_usage，以及开发人员专用的精确授权，见 [开发人员诊断](developer-diagnostics.md)。命令名和任意参数不能由客户端传入：

```json
{
  "namespace": "wx-test-ns-001",
  "pod": "redis-demo-0",
  "container": "redis",
  "action": "redis_ping",
  "timeoutSeconds": 10
}
```

| action | 服务端生成的 argv |
| --- | --- |
| redis_ping | ["redis-cli", "PING"] |
| nginx_check | ["nginx", "-t"] |
| disk_usage | ["df", "-h", "/"] |
| process_list | ["ls", "-1", "/proc"]；无需目标安装 ps |

这不是交互式 shell，不提供 stdin、TTY、sh -c、任意 argv、主机命令或节点终端。容器需要已有对应程序；不自动安装程序、启动调试容器或拉取镜像。带密码 Redis 的 redis_ping 可能返回 NOAUTH，本工具不会读取或注入密码，文本内容需由调用方判断。

返回字段：output、upstreamError、truncated、timedOut、outcome、podUID、durationMs。上游 v0.1.2 成功时优先返回 stdout，无 stdout 时返回 stderr，出错时返回工具错误文本；它不提供独立 stdout/stderr 或机器可读退出码，因此这里保留文本及 isError，不构造 exitCode。completed 表示上游调用完成，不代表输出业务检查通过。

默认 10 秒，允许 1–30 秒；同一预算覆盖授权、Pod 检查、MCP 初始化及命令执行，durationMs 统计整个用例耗时。检查或初始化期间超时返回 timedOut=true、outcome=not_executed；提交执行后超时返回 outcome=unknown。桥接输出限制 256 KiB，HTTP 协议响应体限制 1 MiB。sidecar 也限制后端输出为 256 KiB；上游可能取消执行并追加文字说明。truncated 仅表示桥接自身截断，不能用于判断上游是否曾截断，需同时阅读原文。timedOut=true / outcome=unknown 表示结果不确定；连接关闭不保证远程进程已经终止。中途传输失败同样视为结果不确定，不自动重试执行。exec 的 MCP 工具标记为非只读且可能有副作用。

## 启用配置

默认配置禁用 exec，不改变现有部署。先按 [认证说明](authentication.md) 准备 JWT/PAT 与 olixops-auth Secret，更新 deployments/catalog.yaml 的 config.yaml：

```yaml
kubernetes:
  in-cluster: true
  write-enabled: true
  allowed-namespaces: [wx-test-ns-001, higress-system]
auth:
  enabled: true
  # company / pat 配置按认证说明填写；凭据仅挂载 Secret，不写入 YAML。
mcp:
  enabled: true
  write-enabled: true
pod-exec:
  enabled: true
  endpoint: http://127.0.0.1:8082/mcp
  allowed-namespaces: [wx-test-ns-001]
```

以上是局部配置示例，保留现有其他字段。日志入口沿用原访问策略；exec 要求 JWT/PAT、Kubernetes 写开关、明确业务 Namespace，以及 HTTP 或允许写工具的 MCP 入口；不再强制要求主 MCP 开启。旧 diagnostics-only 模式保持原限制。exec Namespace 在主 allowlist 非空时必须是其子集，否则启动失败；运行时仍执行 kubernetes.allowed-namespaces 检查；default、olixops、kube-* 禁止 exec。

准备目标 Pod 的声明或工作负载 spec.template.metadata.labels：

```yaml
k8s-ctl/exec-enabled: "true"
```

执行前检查标签、指定容器存在且正在运行，拒绝终止中的 Pod、控制服务自身、hostNetwork/hostPID/hostIPC、hostPath，以及普通/init/ephemeral 容器中的 privileged。检查不等同于完整沙箱隔离，Pod UID 仅记录检查时观察值；Pod exec API 没有 UID precondition，检查与执行之间仍有同名替换竞态。

按目标 Namespace 修改 deployments/components/kubernetes-mcp/rbac.example.yaml 的 demo，再由集群管理员审核应用。示例只授予 get pods 和 get/create pods/exec：上游使用 WebSocket GET，并可回退 SPDY POST。**该 RBAC 示例不自动包含在部署包中**，也不授予集群范围 exec。原有资源、日志与 API discovery 权限继续依赖现有 RBAC。

本地查看含 sidecar 的部署清单（不访问集群，不拉镜像）：

```bash
kubectl kustomize deployments
```

部署时选择 kubectl apply -k deployments；直接 apply -f deployments/catalog.yaml 仍是单主容器版本。Kustomize 组件位于 deployments/components/kubernetes-mcp，TOML 唯一配置来源为该目录下的 config.toml，由 Kustomize 生成 ConfigMap，不再维护第二份副本。先在有镜像访问能力的环境将固定版本镜像同步到内部仓库并修改 sidecar.yaml，再发布；本次开发未拉取或构建镜像。

主服务启动时不要求 sidecar 已就绪；调用时未就绪返回错误。现有 /ready 不能证明 sidecar、pods/exec 权限或具体诊断命令可用，需真实集群验收。主程序和配置更新后需重建主镜像并滚动重启，不能只加 sidecar 而沿用未包含桥接代码的旧镜像。

开启认证后，客户端继续只配置 k8s-ctl 的 /k8sctl/api/v1/mcp 与 Bearer PAT，不新增 sidecar MCP 地址。REST 同样使用 Bearer JWT/PAT 和现有 code/msg/data 响应；MCP 使用标准协议结果。

## 权限与验收边界

调用已有 Authorizer 的 pod.exec（Write=true）扩展点，可信用户身份来自认证上下文。当前默认 Authorizer 仍放行，尚未实现个人 Namespace/Pod 归属、角色审批或逐用户 Kubernetes 身份。配置、标签与 RBAC 是当前作用范围限制，不能宣称已经有完整多人隔离。记录用户 ID、PAT ID、目标、固定动作、结果与成功标记，不记录令牌、完整输出或任意命令参数；完整持久化审计未实现。

自动测试使用本地模拟 Kubernetes API 和真实 SDK 协议服务，覆盖 JWT REST、PAT MCP、授权扩展点、固定动作、参数拒绝、Namespace 和 Pod 检查、上游错误、输出限制、连接错误与超时。它们验证桥接逻辑，不代替上游镜像和集群联调。

真实验收由操作者启用配置后，用已有、明确允许的开发 Pod 调用日志和固定动作；验证 RBAC 不足、无标签、非法动作均被拒绝，并检查上游版本和网络行为。本次未修改真实集群、授予 RBAC、部署 sidecar 或执行真实容器命令。

## 源码导航

- internal/domain/pod_exec.go：执行端口与输入/结果模型。
- internal/application/pod_exec.go：身份、Namespace、固定动作、写开关和授权编排。
- internal/infrastructure/kubernetes/pod_exec.go：执行目标检查。
- internal/infrastructure/k8smcp/client.go：上游 MCP SDK 适配器与传输限制。
- internal/interfaces/mcp/pod_exec.go：对外 exec_pod_command 注册。
- internal/interfaces/handler/pod_diagnostics.go：日志与执行 HTTP 入口。


开发人员使用的两个诊断工具、按用户精确授权、managed 本地 MCP 验证和文件读取新增边界，见 [开发人员 MCP 诊断](developer-diagnostics.md)。该模式与完整管理工具集分开部署。
