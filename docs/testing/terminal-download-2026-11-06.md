> 此记录描述旧版 Base64 下载实现；HTTP 流式下载已取代旧 1 MiB/Perl 方案，最新结果见 [HTTP 下载测试](http-download-2026-11-06.md)。

# 主 MCP 终端与文件下载验证（2026-11-06）

本轮实现持续 stdin/TTY 会话、增量输出读取和单文件下载，均注册在原认证主 MCP。验证使用本地模拟 Kubernetes API、模拟持续执行流和真实 Go MCP SDK；未把模拟测试声称为真实集群 PTY、SPDY 网络链路或远程进程终止验收。

## 已验证

- 主 MCP 同时发现原管理工具、单次 exec、四个终端会话工具和 download_pod_file。
- 一个会话连续输入、增量读取、字节游标；输出 Base64 保留原始控制字符。
- 其他用户不能输入、读取或关闭会话。
- 用户会话配额、空闲清理、进程退出清理、写入阻塞超时与缓冲溢出丢失字节提示。
- 写入超时关闭连接，remoteExitConfirmed=false，不声称已终止远程进程。
- Pod UID 替换后拒绝后续输入并删除本地会话。
- 拦截器拒绝输入或下载；下载拒绝后模拟执行后端调用数不增加。
- 二进制文件含 NUL、非 UTF-8 字节，通过 Base64 往返保持内容；响应包含大小及 SHA-256。
- 超过 1 MiB 的文件拒绝，不返回截断成功；保留系统路径拒绝。
- 默认终端限制不被 macOS 的 SHELL 环境变量覆盖；显式 K8S_CTL_SHELL_* 覆盖仍生效。

## 检查结果

- gofmt：通过。
- go test ./... -count=1 -timeout 120s：通过。
- go vet ./...：通过。
- 新增会话与 MCP/下载集成测试的 go test -race：通过。
- git diff --check：通过。

终端管理测试位于 internal/application/shell/sessions_test.go；主 MCP 与下载集成测试位于 tests/terminal_test.go。实际集群还需核对 pods/exec create RBAC、SPDY 支持、目标 /bin/sh 与下载所需 Perl/Fcntl。没有拉取调试镜像或安装目标容器依赖。
