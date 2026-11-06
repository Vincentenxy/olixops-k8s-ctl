# HTTP 流式上传验证（2026-11-06）

新增主 MCP 工具 upload_pod_file 与认证 HTTP POST /api/v1/cluster/pods/files/upload。文件不进入模型上下文；无固定大小上限，只创建新文件，已有目标不覆盖，不支持断点续传。

## 自动化集成验证

通过 SDK 连接主 MCP 准备引用，再通过真实 HTTP 测试服务发送原始文件流，验证：

- 未认证、其他 UserID 拒绝且不消费有效引用；Content-Range 拒绝；一次成功使用后引用不能重复领取。
- 二进制（NUL/非 UTF-8）、超过 1 MiB 的文件、空文件上传成功；返回 JSON envelope、实际字节数、SHA-256、Pod UID。
- 独立 upload_file 动作，download_file 不自动获得上传权限；实际上传时重新检查集中拦截器与 Pod UID。
- 输入读取中断或接收阶段失败时不发布最终文件，并尽力清理临时文件。
- 容器端临时目录准备失败时，在读取大请求前拒绝；测试包含 Expect: 100-continue 的大请求。
- 文件系统测试后端实际运行服务端固定脚本，在测试专用临时目录验证 0600 权限、二进制内容、原文件保持不变、已有目录/符号链接拒绝、临时目录清理。仅此测试适配器在本机执行脚本；生产代码始终通过 Kubernetes stream backend 在容器内执行。

## 真实集群验证与限制

构建独立本地实例监听 127.0.0.1:18094，复用现有 PAT 与服务端 Provider，通过主 MCP 准备，随后 curl 上传约 2 MiB 文件。没有重启用户正在运行的 8091 服务，没有修改 Pod 部署、安装工具或拉取镜像。

实际目标 env-vault / env-vault-2 / env-vault 使用只读根文件系统，/tmp 也只读，没有符合当前路径范围的可写测试目录。MCP Shell 返回的 mkdir 错误明确为 Read-only file system。因此本次只验收了真实集群的拒绝行为：修复后 0.11 秒返回 HTTP 400，未创建最终文件。

首次测试发现大 stdin 连接上的命令如果因只读文件系统提前退出，部分运行时的流可能持续等待；调整为先用不带 stdin 的 exec 准备可写临时目录，失败立即拒绝，不开始文件流。该行为已增加自动化回归测试。

后续已部署可写临时测试 Pod，完成真实上传与下载回读验收；见 [真实可写 Pod 验证](file-transfer-demo-2026-11-06.md)。原 env-vault-2 保持只读。

GB 级、慢上传、传输过程中磁盘耗尽、并发、断点续传及多副本均未验收。后续事项见 [开发待办](../roadmap.md)。


## 最终检查

- gofmt 已执行。
- `go test ./... -count=1 -timeout 120s` 通过。
- `go vet ./...` 通过。
- `go test -race ./tests -run 'TestUploadThroughMainMCP|TestTerminalAndBinaryDownloadThroughMainMCP' -count=1 -timeout 120s` 通过。
- `git diff --check` 通过。

独立验证实例已停止；保留用户原 8091 服务状态。没有提交或推送 Git。
