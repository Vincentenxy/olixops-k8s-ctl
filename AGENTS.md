# olixops-k8s-ctl 开发规范

- HTTP 接口使用 `/api/v1/pub/...` 表示无需认证的公开接口；其他 `/api/v1/...` 接口接入认证后才能开放。
- 无参数查询使用 GET；带参数操作使用 POST；不使用 DELETE/PUT 作为业务接口。
- 响应统一为 `{"code":0,"msg":"","data":{}}`，HTTP 错误也保持同一结构。
- 全局日志使用 `pkg/logger`，请求使用 `X-Request-Id` 并在响应中回写。
- 配置通过 Viper 加载，凭据只能通过环境变量、挂载文件或 Kubernetes Secret 注入，不提交真实 Secret。
- Kubernetes 控制能力必须限制集群、Namespace、资源类型和操作；禁止执行任意用户命令或直接返回 kubeconfig。kubectl/Helm 只能通过参数数组调用，不得拼接 shell 命令。
- kubeconfig 默认读取当前用户 `~/.kube/config`，绝不打印文件内容、token、证书或连接串；控制服务默认监听回环地址，修改为非回环地址必须启用 API key。
- kubeconfig 通过 `internal/infrastructure/kubeconfig.Provider` 读取；文件、数据库和 Secret Manager 只能作为 Provider 实现，client-go、kubectl、Helm 不得直接依赖具体存储路径。数据库 Provider 必须在持久化和读取时完成加密、权限校验和轮换，不把原文写入日志。
- 业务模块按 domain、application、interfaces、infrastructure 分层；外部 Kubernetes API 通过 adapter 隔离。
- 代码变更运行 `gofmt`、`go test ./...` 和 `go vet ./...`；接口变更同步 README 与测试。
