# HTTP 流式文件下载验证（2026-11-06）

本次将 download_pod_file 从旧版 Base64/1 MiB/Perl 方案改为 MCP 准备 + 认证 HTTP 下载。以下为实际测试结果，未使用开发人员本机 kubeconfig 执行下载，也没有在目标容器安装程序或拉取镜像。

## 实际集群链路

使用当前代码构建独立本地服务，监听 127.0.0.1:18094，不替换用户正在运行的 8091 服务。复用本地认证配置及现有有效 PAT，集群凭据仅由服务端 Provider 使用；关闭测试实例的 catalog 后台任务。测试后关闭独立实例。PAT、配置文件原文不写入测试报告。

链路：通过 HTTP JSON-RPC 调用主 MCP 的 download_pod_file，取得用户绑定的下载引用，随后使用 curl POST /api/v1/cluster/pods/files/download 保存原始文件。

| 目标 | 大小 | 结果 |
| --- | ---: | --- |
| env-vault / env-vault-2 / env-vault，/app/env-vault | 40,906,914 字节 | 成功保存，SHA-256 与此前核验值一致 |
| 同一容器，/app/configs/config.yaml | 6,219 字节 | 成功保存，支持 ConfigMap 符号链接 |

主程序 SHA-256：`b3ae0a0ed604eaef65f05fc7fab741fe850a6fb71a9d7a59c4b334c0ab1cab05`。

文件仅保存在忽略目录 .local/manual-mcp-test/，权限 0600；没有执行下载的程序。配置原文可能包含凭据，不在本文展示。

## 自动化检查

- 主 MCP 发现原有工具、终端会话工具及 download_pod_file；准备结果只含下载元数据。
- 无认证 HTTP 请求拒绝，其他 UserID 无法领取引用，原用户随后仍可下载。
- Range 请求拒绝且不会消费引用；一次成功使用后重复请求拒绝。
- 普通二进制（含 NUL 和非 UTF-8）、大于旧 1 MiB 上限的文件、空文件成功流式下载。
- 拦截器拒绝准备时不执行容器命令；Pod UID 改变后拒绝领取，不连接替换后的 Pod。
- 容器在输出文件内容后报告失败时，HTTP 连接中断，客户端读取返回错误，不把部分或未确认完成的输出作为完整成功。
- 外部下载地址配置加载与校验：支持带网关前缀的 HTTPS、回环 HTTP，拒绝用户凭据、query、fragment 等配置。

验证结果：gofmt 已执行；`go test ./... -count=1 -timeout 120s`、`go vet ./...`、`go test -race ./tests -run TestTerminalAndBinaryDownloadThroughMainMCP -count=1 -timeout 120s` 均通过，`git diff --check` 通过。

## 验证边界

39 MiB 成功不能代表 GB 级文件或数百人并发已完成验收。本次没有做下载并发压测、Higress 长连接/缓冲专项测试、磁盘耗尽测试或断点续传；未承诺文件在读取期间保持不变。当前仍为单实例内存下载引用。上述事项记录在 [开发待办](../roadmap.md) 的下载第二阶段。
