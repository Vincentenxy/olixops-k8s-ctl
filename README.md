# olixops-k8s-ctl

Kubernetes 控制服务。通过当前用户 kubeconfig 建立 client-go 连接，提供基础查询和受控的 kubectl/Helm 操作；服务默认只监听本机回环地址。

## 当前能力

- Go 1.26 + Gin 1.12 + Viper 1.21 + Zap 1.28 + Kubernetes client-go 0.28.15（匹配当前 v1.28 集群）。
- `GET /api/v1/pub/health`：进程健康检查。
- `GET /api/v1/pub/ready`：HTTP 服务已启动。
- `GET /api/v1/cluster/status`：Kubernetes 服务端版本和 Namespace 数量。
- `GET /api/v1/cluster/namespaces`：Namespace 列表。
- `GET /api/v1/cluster/storage-classes`：StorageClass 列表。
- `GET /api/v1/cluster/namespaces/{namespace}/resources`：Deployment、StatefulSet、Pod、Service、PVC 摘要。
- `GET /api/v1/cluster/namespaces/{namespace}/gateway-resources`：GatewayClass、Gateway、TCPRoute 摘要。
- `POST /api/v1/cluster/kubectl/apply`：校验后调用 `kubectl apply`，支持 server-side 和 server dry-run。
- `POST /api/v1/cluster/helm/upgrade`：调用 `helm upgrade --install`，支持 values YAML 临时文件和等待。
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

默认从当前用户的 `~/.kube/config` 读取 kubeconfig，并使用当前 context。也可以通过配置或环境变量覆盖：

```powershell
$env:KUBERNETES_KUBECONFIG = "$env:USERPROFILE\.kube\config"
$env:KUBERNETES_CONTEXT = "kubernetes-admin@kubernetes"
$env:KUBERNETES_WRITE_ENABLED = "true"
go run ./cmd
```

控制接口默认不要求 API key，因为服务默认绑定 `127.0.0.1`。如果将 `SERVER_HOST` 改为非回环地址，必须配置 `CONTROL_API_KEY`，请求携带 `X-Olixops-Control-Key`。生产环境不能在公网暴露未配置认证的控制接口。

kubectl apply 请求只接受受控资源类型（ConfigMap、Deployment、Service、StatefulSet、PVC、Gateway、HTTPRoute、TCPRoute），拒绝 Secret 和未收录的资源；请求中的 Namespace 必须与 URL/参数一致。Helm 和 kubectl 都使用参数数组执行，不经过 shell。密码和 kubeconfig 不会写入日志或响应。

示例：

```powershell
$body = @{ namespace = "default"; dryRun = $true; manifest = @"
apiVersion: v1
kind: ConfigMap
metadata:
  name: example
  namespace: default
data:
  check: "true"
"@ } | ConvertTo-Json -Compress
Invoke-WebRequest http://127.0.0.1:8091/api/v1/cluster/kubectl/apply `
  -Method POST -ContentType application/json -Body $body
```

## 目录

```text
cmd/                         服务入口
configs/                     Viper 配置
internal/application/        Kubernetes 控制用例、清单校验和权限边界
internal/domain/             Kubernetes 领域契约（持续扩展）
internal/infrastructure/    kubeconfig、client-go、kubectl/Helm 适配
internal/interfaces/         HTTP handler、middleware、router
pkg/logger/                  Zap 日志
pkg/response/                统一响应
tests/                       HTTP 契约测试
```

项目不会在 HTTP 请求中执行任意用户命令；当前 apply/Helm 接口仍应只在受控本机或已配置 API key 的网络中使用。后续将补齐用户认证、集群注册、Namespace 归属、审计和更细的资源白名单。
