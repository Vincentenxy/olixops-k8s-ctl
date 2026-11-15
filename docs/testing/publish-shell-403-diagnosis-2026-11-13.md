# publish Shell 403 排查记录

日期：2026-11-13。本轮只排查，不修改权限策略、Pod 标签、grants 或集群资源。

## 结论

本次请求被 k8s-ctl 的 Pod 执行资格检查拒绝。目标 Pod 缺少 `k8s-ctl/exec-enabled=true` 标签，控制器配置中的 `pod-exec.grants` 为空，不能提供获准的 Pod UID。当前代码在这两个条件均不满足时返回 `fault.Forbidden("Pod not eligible for exec")`。

此结论来自真实 MCP 复现、实时 Pod 元数据、当前配置和执行路径的交叉核对。上层错误适配器隐藏了具体消息，因此 MCP 原始响应只显示 `[forbidden] 下游调用失败，HTTP 403`，并未直接返回上述英文消息。

## 真实复现

- 入口：正在运行的 IDEA publish 项目 `/pubops/mcp`，通过 MCP initialize 和 tools/call 调用，没有改用 REST 执行业务操作。
- 用户：10121993。
- 服务：2963078505，环境 sim。
- 集群：gdl-dev-lucy；Namespace：setting-efficient。
- Pod：setting-efficient-api-6cdb858d56-6nrf8；容器：setting-efficient-api。
- 工具：`k8sctl-exec-service-command`。
- 测试命令：`printf k8sctl-chain-ok`，timeoutSeconds=5。
- 结果：isError=true、code=-1、`[forbidden] 下游调用失败，HTTP 403`。
- 关联请求 ID：`3f910d50-fc7e-498f-b314-7f80c7744d83`。
- 下层审计：userId=10121993、action=shell、outcome=not_executed、success=false；`POST /api/v1/cluster/pods/exec` 返回 403。

## 链路核对

| 阶段 | 证据与结论 |
| --- | --- |
| publish MCP 身份、服务解析与授权调用 | `ServiceContainerService.exec` 先调用 `ServiceObservationService.select`；后者执行服务操作检查、服务 Pod 归属检查和工作负载授权。该请求实际到达下层 exec，说明这些前置步骤未拒绝本次调用。 |
| 下层认证 | exec 审计记录了 userId=10121993，说明身份已进入应用层。 |
| 能力开关 | 当前私有配置中 pod-exec.enabled、shell.enabled、kubernetes.write-enabled 均为 true。 |
| Namespace 静态名单 | 已移除；本次不是 Namespace 名单拒绝。同一请求的资源列表、Pod 详情查询均为 200。 |
| grants | 当前 grants=[]；操作器只在列表非空时安装 grants 策略。因此不是“grants 策略开启后未匹配用户”的拒绝，而是未提供可替代标签的 UID 凭据。 |
| Authorizer | 操作器未安装自定义 WithAuthorizer，默认 DeferredAuthorizer 放行。 |
| Pod 执行资格 | 实时 Pod 缺少 exec-enabled 标签；批准 UID 列表为空。两者共同命中 CheckPodExecTarget 的拒绝条件。 |
| Shell 命令拦截器 | 当前实现为 AllowAll，且执行顺序在 Pod 资格检查之后；本次未到该步骤。 |
| Kubernetes pods/exec | 命令尚未提交。此次不能证明 Kubernetes pods/exec RBAC 已获准，也没有证据表明本次 403 来自 Kubernetes exec。 |

实时 Pod 仍在运行，无删除标记，不使用 hostNetwork、hostPID、hostIPC，无 hostPath 卷或 privileged 容器，也不是控制器自身 Pod。上述其他资格条件未命中。

## 关键源码

- `internal/infrastructure/kubernetes/pod_exec.go`：`CheckPodExecTarget` 中强制要求 exec-enabled 标签或批准 UID。
- `internal/application/pod_exec.go`：先检查 Pod 资格，再调用 Shell 拦截器和实际执行后端。
- `cmd/operator/main.go`：grants 非空才调用 WithDiagnosticGrants；Shell 拦截器为 AllowAll。
- `internal/application/terminal.go`：shellTarget 共用 CheckPodExecTarget。
- `internal/application/download.go`、`upload.go`：文件准备和传输复核复用 shellTarget，因此同样受此门槛影响。
- 上层 `src/main/java/net/gtjaqh/pubdevops/service/k8sctl/CtlHttp.java`：错误响应只提取 data.reason，丢弃具体消息，转为通用 HTTP 错误提示。

## 验证与限制

`query-service-deploy-status` 的真实 MCP 调用成功，用于回读当前 Pod YAML，仅提取执行资格相关字段。私有原始响应未写入文档。

以下定向测试通过：

```sh
go test ./internal/infrastructure/kubernetes -run 'TestExecTargetEligibility|TestExplicitDiagnosticUIDOptIn' -count=1 -v
```

覆盖标签缺失、匹配/不匹配 UID、宿主机访问、特权容器、运行状态和删除状态。测试证明现有规则的行为；不代替真实 Shell 执行验收。

本轮未修改业务代码或权限配置，未执行容器写入，未创建 a.txt，未验证 pods/exec RBAC。后续若按平台服务权限统一授权，需要单独调整该强制标签/静态 UID 的策略边界，并补充可识别的安全错误信息；取消 Namespace 名单并不会自动取消此规则。
