# HTTP / MCP 代码审核修复与回归记录

日期：2026-11-07。范围：上一轮审核发现的五项边界问题与三项复用建议。没有新增业务接口、修改 MCP 工具名或新增资源归属权限；现有成功响应继续由原适配器返回。

## 修复与证据

| 问题 | 修复行为 | 回归证据 |
| --- | --- | --- |
| 上传接收端提前失败，客户端停发时 Body.Close 卡住 | HTTP 层在关闭未读完的 body 前打断网络读取，使接收协程退出并进入暂存清理；不新增文件大小或传输总时长上限 | `tests/upload_cancellation_test.go` 使用真实本机 TCP：声明 1 MiB，只发一个字节，远端模拟失败；断开客户端之前 handler 已返回且清理已执行 |
| 终端输入排队等待互斥锁不响应取消 | 用可响应 context 的会话写入许可串行化输入；取消排队请求不关闭活动会话 | `internal/application/shell/cancellation_test.go` 阻塞首个写入，第二个写入超时返回，原会话仍 open |
| kubectl/Helm 丢失超时原因 | 保留 context Cause；区分未开始变更、只读调用与已经启动的变更；后者返回 execution_unknown，不重试 | `tests/command_errors_test.go` 模拟 CLI 子进程，覆盖只读、apply、dry-run、工作负载回滚、Helm 回滚及执行前取消；核对变更只调用一次 |
| Catalog 错误统一映射 400 | 保留已有业务原因码，按不存在、冲突、拒绝、结果未知、依赖不可用映射状态 | `tests/catalog_interfaces_test.go` 验证缺失模板/实例 404、配置版本冲突 409；`api/errors_test.go` 覆盖业务分类 |
| 慢速或不完整 JSON 长期占用连接 | 读取阶段最多 10 秒，与更早的操作预算取最小；读完清除网络 deadline；HTTP/MCP 使用同一读取实现，各自保留协议解码 | HTTP 和 MCP 的真实 TCP 不完整请求测试；`httpio/body_test.go` 覆盖 HTTP/1.1、HTTP/2 的慢请求和正常连接复用 |

本轮先添加回归用例重现上述问题，再修改实现。全量测试发现零字节上传可能早于读取协程完成，补充了空 body 状态识别。Race 运行还发现 socket 超时会连带取消 request context，已优先保留原始超时原因；CLI 测试关闭子进程 race runtime 默认退出等待，避免把前置只读检查超时误当成变更中断。修正后的关键网络/命令测试在 race 下连续三轮通过。

## 复用整理

- `application/api.TerminalService` 成为 HTTP/MCP 共用的终端与文件准备契约，原接口名保留为别名。
- MCP 终端注册改用通用 `registerTool`，共用超时、日志、错误映射与注解设置。
- 上传和下载分别持有共享实现 `ticketStore`，统一用户绑定、五分钟有效期、容量清理与单次消费；执行前再次验证有效期。测试覆盖跨用户拒绝、32 个并发消费者只能成功一次，以及校验后过期的拒绝。

## 验证命令

```sh
go test ./... -count=1
go vet ./...
go test -race ./internal/application ./internal/application/api ./internal/application/shell ./internal/interfaces/httpio ./internal/interfaces/handler ./internal/interfaces/mcp ./tests -count=1
git diff --check
```

最终结果：以上命令全部通过；race 检查未报告数据竞争。全部 Go 包测试通过，集成测试包耗时约 16.5 秒；race 集成测试包耗时约 59.7 秒。`go vet` 无输出。所有新增和修改的 Go 文件使用 gofmt 格式化，`git diff --check` 无格式错误。

测试覆盖现有 HTTP-only、MCP、身份隔离、终端、文件传输和上层模拟代理用例；文件回归包含零字节、二进制、完整性校验、截断、取消与一次性引用。关键新增网络/命令用例另执行 `-race -count=3`。

## 环境边界与后续

本轮使用本机 httptest HTTP/HTTPS、真实 TCP 连接、临时 SQLite、模拟 Kubernetes/exec 后端及测试 CLI 子进程。未访问真实集群、拉取镜像、向业务 Pod 安装工具或修改业务数据。此前真实集群记录仍见 [双入口测试记录](http-operator-2026-11-06.md)，不把本轮模拟结果替代真实集群验收。

远端暂存清理仍为尽力执行：Pod 不可达、被替换或权限撤销时可能无法删除残留。普通文件流仍无固定大小上限、断点续传和并发容量治理；这些继续保留在后续待办。上层代理还需配置自己的连接与转发超时。

采用新代码需要重新编译并重启服务（集群部署则构建并更新镜像）。终端与文件引用仍只保存在进程内，重启后需要重新打开或准备。
