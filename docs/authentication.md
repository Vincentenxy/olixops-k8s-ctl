# JWT 与个人 PAT 认证

更新：2026-11-19。本文区分平台操作器模式和独立兼容模式。统一 `cmd/main.go` 通过 `app.mode` 选择运行模式，Publish 与操作器之间使用当前调用者的同一个 JWT 或 PAT。独立模式仍保留旧 Control API key 兼容能力，不能把独立兼容配置带入平台操作器。

## 平台操作器认证链路

启用 `auth.enabled` 后，HTTP 和 MCP 入口都只接受以下请求头：

```text
Authorization: Bearer <JWT 或 PAT>
```

每个请求都会重新认证。服务不接受 Cookie、query 参数、请求体中的 userId 或 `X-User-Id` 作为身份来源。认证成功后，可信 UserID、StaffNo、Name 和认证类型写入请求 context，业务代码通过 `internal/domain/auth.FromContext` 读取。

JWT 认证使用配置的 RSA 公钥，只允许 RS256。必须存在有效的 `exp`。平台操作器启用 JWT 时必须同时配置 issuer 和 audience，并逐项校验 key-id（配置时校验）。RSA 公钥至少 2048 位。用户标识使用 `staffuserid`，该字段为空时请求被拒绝。`name` 和 `staffno` 作为身份补充字段传递，不用于替代 UserID。

PAT 是由平台 Setting 使用 PAT 专用 RSA 私钥签发的 JWT。操作器使用 `auth.pat.public-key` 校验 PAT，使用 `auth.jwt.public-key` 校验公司 JWT。操作器校验 RS256 签名、`kid`、`iss`、`aud`、`iat`、`exp`、`sub`、`staffuserid`、`authSource`、`tokenUse` 和 `typ`，不签发、修改或撤销 PAT，也不访问 Setting、Redis 或本地 PAT 数据库。PAT 的撤销、账号状态和业务权限由 Publish 在转发前检查；操作器只接受经过 Publish 授权链路到达的签名凭据。

Publish 先使用当前用户的 JWT/PAT 执行服务、环境和工作负载权限判断，然后把同一个 Bearer Token 发送给操作器。Publish 不签发短期内部 JWT，不维护独立注册控制 Token，也不在操作器之间复制 PAT 数据库。操作器注册接口和集群业务接口使用同一认证链路，资源权限仍由 Publish 负责，操作器继续执行目标、会话、Pod UID 和 Kubernetes RBAC 校验。

认证失败返回 HTTP 401，并带 `WWW-Authenticate: Bearer`。JWT/PAT 验证配置不可用返回 HTTP 503。错误正文不包含完整令牌、Redis 内容、公钥或 kubeconfig。

## 操作器配置

平台操作器需要配置平台 JWT 公钥。实际部署通过私有 ConfigMap、Secret 或环境变量注入，示例文件不填写真实值：

```yaml
auth:
  enabled: true
  jwt:
    enabled: true
    public-key: ""
    issuer: ""
    audience: ""
    key-id: ""
  pat:
    enabled: true                 # PAT 使用独立公钥和 Token 元数据
    public-key: ""
    issuer: efficient-platform
    audience: platform-api
    key-id: platform-pat-v1
```

只使用 JWT 时可以关闭 PAT。只使用 PAT 时可以关闭 JWT。启用哪类认证就配置对应的公钥、issuer、audience 和可选 key-id。操作器模式启用 JWT 时必须填写公司 JWT 的 issuer 和 audience，两种认证都关闭时程序拒绝启动。操作器不读取 `auth.company`、`auth.pat.private-key-file`、`auth.pat.database-path` 或 Redis 认证配置，这些旧配置会被配置加载器拒绝，避免误以为认证仍由操作器本地签发和存储。

认证配置只在启动时加载。修改公钥、issuer、audience 或 key-id 后重启操作器。Publish 的 `efficient.k8s-ctl.operator.base-url` 仅配置操作器地址，不配置认证凭据。

## Publish 调用示例

业务请求由 Publish 产生，调用者只需要向 Publish 提供自己的 JWT 或 PAT：

```text
POST /api/v1/operator/clusters/configure
Authorization: Bearer <当前调用者 JWT 或 PAT>

POST /clusters/{clusterId}/api/v1/cluster/pods/logs
Authorization: Bearer <当前调用者 JWT 或 PAT>
```

注册 body 只包含 `clusterId` 和已由 Publish 解密得到的 kubeconfig。操作器返回集群标识，不返回 kubeconfig 或其他凭据。Publish 不能接受客户端指定的身份 Header，也不能把一个用户的 Token 缓存在所有用户共用的默认请求头中。

## 独立兼容模式

独立 `cmd/main.go` 仍支持 `auth.enabled=false` 的旧模式。此模式监听非回环地址时必须配置 `control.api-key` 或 `control.api-key-file`，业务 REST 通过 `X-Olixops-Control-Key` 认证。该模式不适用于 Publish 动态操作器，也不能用于操作器注册入口。

个人 PAT 的签发、列表、撤销和生命周期由平台 Setting 负责。k8s-ctl 不提供本地签发命令，也不挂载 PAT 私钥、PAT SQLite 或 Redis。公司 JWT 使用 `auth.jwt.public-key`，平台 PAT 使用独立的 `auth.pat.public-key`，两套 issuer、audience 和 key-id 也分别配置。部署方式见 [HTTP 操作器指南](http-operator-guide.md) 和 [Publish 接入说明](platform-local-operator.md)。

## 运行检查

启动后可先访问健康检查，健康检查不代表业务认证成功：

```bash
curl --fail-with-body https://operator.example/k8sctl/api/v1/pub/health
curl --fail-with-body https://operator.example/k8sctl/api/v1/operator/capabilities \
  -H "Authorization: Bearer ${PLATFORM_TOKEN}"
```

排查顺序如下。

1. 检查请求是否只有一个标准 `Authorization` header，并确认值为 `Bearer <token>`。
2. JWT 请求检查 RS256、公钥、`staffuserid`、`exp`、issuer、audience 和 key-id 是否一致。
3. PAT 请求使用相同的 JWT 验证规则检查签名和 PAT 声明；撤销与业务权限由 Publish 在操作器之前完成。
4. 检查操作器与 Publish 使用的当前 Token 是否属于同一调用者，不能使用旧的内部 JWT 或独立注册 key。
5. 检查 Publish 业务权限和 Kubernetes RBAC。认证成功只说明调用者身份可信，不代表可以访问任意服务、Namespace 或 Pod。

日志只允许打印请求 ID、路径、HTTP 状态、耗时、集群 ID 和错误原因标识。禁止打印 Authorization、PAT、JWT、公钥、kubeconfig 和命令正文。

## 验证范围

认证单元测试覆盖 JWT 算法、公钥、claims、过期、issuer/audience/key-id、PAT 类型和签名错误。HTTP 测试覆盖 JWT、PAT、401、503、注册入口和同一凭据向业务入口透传。Publish 测试覆盖注册、日志、终端、文件代理和重启后的重新注册。真实操作器和真实 Kubernetes 集群仍需按部署环境执行联调。
