# olixops-k8s-ctl 开发规范

- HTTP 接口使用 `/api/v1/pub/...` 表示无需认证的公开接口；其他 `/api/v1/...` 接口接入认证后才能开放。
- 无参数查询使用 GET；带参数操作使用 POST；不使用 DELETE/PUT 作为业务接口。
- 响应统一为 `{"code":0,"msg":"","data":{}}`，HTTP 错误也保持同一结构。
- 全局日志使用 `pkg/logger`，请求使用 `X-Request-Id` 并在响应中回写。
- 配置通过 Viper 加载，凭据只能通过环境变量、挂载文件或 Kubernetes Secret 注入，不提交真实 Secret。
- Kubernetes 控制能力必须限制集群、Namespace、资源类型和操作；禁止执行任意用户命令或直接返回 kubeconfig。
- 业务模块按 domain、application、interfaces、infrastructure 分层；外部 Kubernetes API 通过 adapter 隔离。
- 代码变更运行 `gofmt`、`go test ./...` 和 `go vet ./...`；接口变更同步 README 与测试。
