# olixops-k8s-ctl

Kubernetes 控制服务的基础项目。当前先提供统一配置、请求链路日志、统一响应和健康检查；Kubernetes client-go 控制能力按开发计划逐步接入。

## 当前能力

- Go 1.26 + Gin 1.12 + Viper 1.21 + Zap 1.28。
- `GET /api/v1/pub/health`：进程健康检查。
- `GET /api/v1/pub/ready`：当前仅表示 HTTP 服务已启动；接入真实 Kubernetes 客户端后再增加集群就绪检查。
- `X-Request-Id` 请求链路透传和访问日志。
- `SERVER_PORT`、`SERVER_MODE` 等环境变量可覆盖配置文件。

## 启动

```powershell
go run ./cmd
```

默认监听 `http://127.0.0.1:8091`：

```powershell
Invoke-WebRequest http://127.0.0.1:8091/api/v1/pub/health
```

环境变量示例：

```powershell
$env:SERVER_PORT = "9091"
go run ./cmd
```

## 目录

```text
cmd/                         服务入口
configs/                     Viper 配置
internal/application/        Kubernetes 控制用例（待接入）
internal/domain/             Kubernetes 领域契约（待接入）
internal/infrastructure/    配置和后续 client-go 适配
internal/interfaces/         HTTP handler、middleware、router
pkg/logger/                  Zap 日志
pkg/response/                统一响应
tests/                       HTTP 契约测试
```

项目不会在 HTTP 请求中执行任意用户提交的 `kubectl` 或 YAML。后续控制能力会按集群、Namespace、资源类型和授权边界提供受控接口。
