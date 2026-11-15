# 临时放行后的 publish MCP 真实链路验收

日期：2026-11-13。接续 [403 排查](publish-shell-403-diagnosis-2026-11-13.md)。本轮用户明确要求先放开权限拦截，测通流程，后续再加权限。

## 改动和生效范围

增加 `pod-exec.authorization-enabled`，缺省为 true，保持既有配置及直接构造客户端的默认行为。当前仓库主配置、平台本地操作器示例及实际本地操作器配置显式设置 false，并将 `shell.interceptor.enabled` 设为 false。标准入口和操作器入口都传递该配置。

关闭目标授权时，不安装静态 grants 策略；Pod 资格检查不再要求 `k8s-ctl/exec-enabled=true` 或批准 UID，不拦截控制器自身 Pod、宿主机网络/PID/IPC、hostPath 或 privileged。此设置仅用于当前联调，恢复策略已记入 [待办](../roadmap.md)。旧 `diagnostics-only` 模式仍要求开启目标授权。

身份认证、publish 原有公共业务权限、服务到 Pod 定位、容器运行状态、删除状态、UID 一致性、会话用户归属、引用有效期、单次消费及传输完整性保持生效。没有关闭 publish 的公共 authz 系统，也没有更改 Kubernetes RBAC 或容器文件系统权限。容器不存在、容器未运行或 Pod 正在删除仍返回对应错误。

本轮没有修改 publish 源码、Pod 标签、集群资源规格或业务配置。重建并重启本轮使用的本地下层操作器，健康检查 code=0；IDEA publish 继续使用现有进程，通过自动注册创建新的集群运行实例。

## 真实 MCP 与文件流验收

用户 10121993；服务 2963078505；环境 sim；集群 gdl-dev-lucy；Namespace setting-efficient；Pod setting-efficient-api-6cdb858d56-6nrf8；容器 setting-efficient-api。

所有命令及文件准备均通过 publish `/pubops/mcp` 调用，不直接调用 Kubernetes，也不绕过平台调用下层 HTTP。文件数据流按 MCP 返回的 publish URL 通过 curl 传输，并携带同一用户认证。

| 能力 | 真实结果 |
| --- | --- |
| k8sctl-get-service-logs | code=0、isError=false；日志原文仅保存于本地私有文件 |
| k8sctl-exec-service-command | `printf k8sctl-chain-ok` 返回预期文本，outcome=completed、upstreamError=false、timedOut=false |
| k8sctl-open-service-terminal | 打开 TTY 会话成功 |
| k8sctl-write-service-terminal / read | 命令执行后读到预期输出，按 nextCursor 继续读取 |
| TTY Ctrl+C | 中断 25 秒 sleep 后，约 2.265 秒读到同一会话中新命令的结果 |
| k8sctl-close-service-terminal | 会话正常关闭 |
| k8sctl-prepare-service-file-upload + publish HTTP 上传 | 65,792 字节二进制和零字节文件均成功，返回字节数与 SHA-256 符合源文件 |
| k8sctl-prepare-service-file-download + publish HTTP 下载 | 两种文件的下载内容与源文件逐字节相同 |
| 单次下载引用重放 | HTTP 404，符合单次使用约定 |
| 清理 | 通过平台 MCP 删除本轮随机 /tmp 测试文件，命令结果 completed，无上游错误或超时；关闭测试终端 |

中断测试中的预期输出不完整出现在输入命令文本中，避免把 TTY 命令回显误判为执行成功。返回标记的耗时小于原 sleep 时间，确认前台任务确实被中断。

以上验证证明此次 kubeconfig 对目标 Pod 的实际 exec 与文件流操作可用；不推断其他 Namespace、其他集群或其他用户具有相同权限。不安装容器工具，不拉取镜像，不修改既有业务文件，不执行安装/卸载或 Pod 重建。

## 自动化检查

- gofmt：通过。
- `go test ./...`：通过。
- `go vet ./...`：通过。
- 定向 race：通过，命令如下；config 包未被该正则选中，其配置加载与默认值测试单独通过。

```sh
go test -race ./internal/infrastructure/kubernetes ./internal/infrastructure/config ./tests \
  -run 'TestDeferredExecAuthorization|TestExecTargetEligibility|TestExplicitDiagnosticUID|TestHTTPExecAuthorizationSwitch|TestOperatorHTTPWithoutMainMCP|TestTerminalAndBinaryDownloadThroughMainMCP|TestUploadThroughMainMCP' -count=1
```

新增测试覆盖开关默认开启、配置显式关闭、diagnostics-only 不可关闭、目标授权可恢复，以及关闭策略后仍拒绝不存在/未运行/正在删除的目标。认证 HTTP 测试使用无放行标签的 Pod，验证开启时 exec、终端及文件准备返回 403，关闭时可用，错误 JWT 在两种设置下均返回 401。

原始 MCP 结果、认证头文件与运行配置在 Git 忽略的本地私有目录，不写入仓库或报告。代码尚未自动提交或推送。
