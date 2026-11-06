# JWT 与个人 PAT 认证

更新：2026-11-06。参考 env-vault 的 RS256、身份字段和 PAT 撤销约定实现；env-vault 本身未修改，也不调用其数据库或 API。

## 当前认证链路

启用 `auth.enabled` 后，业务 REST 与 MCP 每个 HTTP 请求都执行相同认证：

1. 仅接受 `Authorization: Bearer <token>`；不使用 Cookie、query 参数、X-User-Id 或请求体中的 userId 认证。
2. 使用配置的 RSA 公钥验证 RS256 签名，校验 issuer、audience、必须存在的 exp、nbf 和存在时的 iat，允许 30 秒 JWT 时钟偏差；配置 key-id 时还校验 kid。RSA 至少 2048 位。
3. 用户标识优先取字符串 staffuserid，为空回退 sub；name 为姓名。可信身份放入请求 context，REST handler、MCP tool 和现有 Authorizer 均可通过 `internal/domain/auth.FromContext` 读取，不保存原始 JWT。
4. 本地 PAT 必须同时具有 authSource=personalToken、tokenUse=personalAccessToken、typ=k8s-ctl-pat+jwt、jti，并由 PAT 的配置密钥签名。公司 JWT 不能冒充本地 PAT。
5. PAT 额外查询 SQLite，核对 jti、用户归属、令牌摘要、数据库过期和撤销状态。数据库错误返回 503，不降级成匿名或只验签放行。数据库过期校验不使用 JWT 的 30 秒宽限。

MCP SDK 仍使用标准 Streamable HTTP 和 JSON-RPC。认证发生在进入 MCP handler 之前；HTTP 401 带 WWW-Authenticate，错误采用项目统一 code/msg/data 结构，不伪装成工具调用成功。初始化、tools/list、tools/call 等每次 HTTP 请求均需携带 Bearer，不因客户端连接过而跳过检查。已开始执行的请求不会因后来撤销 PAT 自动取消，后续请求会被拒绝。

认证不等于资源授权。本次实现个人令牌管理的归属校验；集群/Namespace/资源动作的细粒度授权仍由现有默认放行的 Authorizer 扩展点负责。ownerId 业务字段尚未与登录身份强制绑定。没有用户目录、用户锁定同步、公司 JWT 主动撤销或全量审计；公司 token 按自身 exp 验证。平台不提供本地密码登录或公司登录页面，由认证网关/统一认证系统签发公司 JWT。

## 配置

合并以下内容到实际读取的 config.yaml；本地配置在 configs/config.yaml，集群配置在 deployments/catalog.yaml 的 ConfigMap。修改配置和密钥后重启，不支持热加载。

```yaml
auth:
  enabled: true
  company:
    enabled: false             # 公司接入时改为 true
    issuer: ""                # 公司 JWT 的实际 iss
    audience: ""              # 公司为本服务分配的 aud
    key-id: ""                # 可选；需要时限制 kid
    public-key: ""            # PEM 或 base64 DER，亦可由 AUTH_COMPANY_PUBLIC_KEY 注入
    public-key-file: ""       # 非空时优先从挂载文件读取
  pat:
    enabled: true
    issuer: k8s-ctl
    audience: k8s-ctl
    key-id: pat-v1
    private-key-file: /app/secrets/auth/pat-private.pem
    public-key-file: /app/secrets/auth/pat-public.pem
    database-path: /data/auth.db
```

本地将密钥路径改为 `.local/auth/pat-private.pem`、`.local/auth/pat-public.pem`，数据库改为 `./data/auth.db`。PAT 签发私钥只从文件加载，不能把真实私钥写入 ConfigMap/Git。缺少密钥、无效密钥、公私钥不匹配或没有启用任何认证来源会启动失败；密钥不会自动生成或覆盖。

本批保留兼容开关：仓库的本地/容器/部署示例默认 `auth.enabled: false`，仍走原 REST Control API key 与公开 MCP。**更新代码或镜像并不会自动开启认证**。完成密钥准备后显式启用；启用时业务 API 只接受 JWT/PAT，旧共享 API key 不作为后门或回退，非回环监听可由 JWT/PAT 认证满足接入要求。Control API key 挂载若仍保留，仍需现有 Secret；也可以在启用认证后移除旧 api-key-file 和对应 volume/mount。

关闭 company 只影响公司 JWT，不影响 PAT；关闭 pat 后不再接受或签发本地 PAT，个人令牌管理返回不可用。每个用户的认证来源应使用同一个稳定 userId。密钥轮换需保留原密钥才能继续接受旧 PAT；当前仅一个 PAT key，不支持轮换宽限期多 key 验签。不要重建签名密钥来做普通升级。

环境变量沿用 Viper，如 AUTH_ENABLED、AUTH_COMPANY_ENABLED、AUTH_COMPANY_ISSUER、AUTH_COMPANY_AUDIENCE、AUTH_COMPANY_KEY_ID、AUTH_COMPANY_PUBLIC_KEY_FILE、AUTH_PAT_ENABLED、AUTH_PAT_PRIVATE_KEY_FILE、AUTH_PAT_PUBLIC_KEY_FILE、AUTH_PAT_DATABASE_PATH；正常部署直接使用配置文件与 Secret，无需依赖环境变量。

## 本地准备与首个 PAT

公司接入之前，由能访问本机配置、PAT 私钥和数据库的管理员离线签发首个 PAT；没有公开的无认证签发接口，也不使用共享 API key 伪造个人身份。管理员需确认 --user-id 的真实归属。

下面命令由管理员按需执行，密钥仅生成一次；仓库不包含真实密钥：

```bash
mkdir -p .local/auth
# 首次准备时确认目标文件不存在，避免覆盖现有签名密钥。
test ! -e .local/auth/pat-private.pem && test ! -e .local/auth/pat-public.pem && (
  umask 077
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out .local/auth/pat-private.pem
  openssl pkey -in .local/auth/pat-private.pem -pubout -out .local/auth/pat-public.pem
)
```

开启配置后执行：

```bash
go run ./cmd/pat --config-dir ./configs --user-id '<真实用户ID>' --name my-mcp --days 30
```

命令输出 `metadata` 和 `token`，token 仅在创建时返回一次，请立即保存到个人凭据管理工具。公司 JWT 可用后，用户通过受保护的创建接口自行申请 PAT，无需管理员逐个签发。此命令拥有本地管理员信任，必须限制私钥和数据库的文件权限。

容器镜像同时包含 `/app/k8sctl-pat`；可通过管理员授权的 Pod exec 在同一 Pod 的配置和持久卷上执行：

```bash
kubectl -n olixops exec deployment/olixops-k8s-ctl -- /app/k8sctl-pat --config-dir /app/configs --user-id '<真实用户ID>' --name my-mcp --days 30
```

集群的密钥挂载使用部署清单中的 `olixops-auth` Secret，键为 pat-private.pem 和 pat-public.pem；先准备 Namespace，再创建 Secret：

```bash
kubectl -n olixops create secret generic olixops-auth \
  --from-file=pat-private.pem=.local/auth/pat-private.pem \
  --from-file=pat-public.pem=.local/auth/pat-public.pem
```

部署示例的 auth-keys volume 在兼容模式下允许 Secret 不存在，开启 PAT 时应用仍会严格要求文件存在。公司公钥也可以放入同一 Secret 的 company-public.pem，配置 public-key-file 指向挂载路径。auth.db 使用现有 /data PVC，不依赖 catalog.enabled；按当前平台的单副本方式运行，升级时保留数据库和签名密钥。

## 接口

所有业务接口继续使用统一 REST envelope；下列接口均需要 Bearer。

| 方法 | 路径 | 功能 |
| --- | --- | --- |
| GET | /api/v1/auth/me | 当前可信用户与令牌类型，不返回原始 JWT |
| POST | /api/v1/user/token/create | 为当前用户创建 PAT，仅允许非 PAT 的已认证身份 |
| GET | /api/v1/user/token/list | 当前用户的令牌元数据，含到期、最后使用和撤销时间 |
| POST | /api/v1/user/token/delete | 撤销当前用户的 PAT，body 为 id；可重复撤销，不能撤销其他用户的令牌 |

创建 body：

```json
{"name":"my-mcp","expiresAt":"2026-12-01T00:00:00Z"}
```

expiresAt 必须晚于当前时间，且最多 365 天；每个用户最多 10 个有效 PAT，数量检查与插入在 SQLite 写事务内执行。名称 1–64 个字符。请求不接受 userId/ownerId 参数。PAT 不能创建另一个 PAT，但可列出和撤销自己的 PAT，包括自身。

返回示例（创建时）：

```json
{"code":0,"msg":"success","data":{"metadata":{"id":"<jti>","ownerId":"<当前用户>","name":"my-mcp","createdAt":"<时间>","expiresAt":"<时间>"},"token":"<仅本次返回的PAT>"}}
```

与 env-vault 的加密保存后可回显不同，这里只存 SHA-256 摘要与元数据，列表不返回令牌，无法恢复遗失的明文；遗失后撤销再创建。认证/令牌接口返回 Cache-Control: no-store，不记录 Authorization、完整 JWT、私钥或令牌摘要。没有新增 PAT 管理 MCP tool，避免在工具输出中分发凭据；PAT 可调用现有全部 MCP 工具，实际动作仍受写开关、Namespace/资源范围和 Kubernetes RBAC 限制。

错误：未认证/过期/无效签名/已撤销为 401；PAT 创建 PAT 为 403；非法请求为 400；他人或不存在的 id 为 404；有效令牌上限为 409；数据库不可用在认证时为 503、管理时为 500。错误响应不包含底层密钥或数据库错误。

## MCP 与 REST 客户端迁移

| 模式 | MCP 路径 | 接入 |
| --- | --- | --- |
| auth.enabled=false | /api/v1/pub/mcp | 旧公开入口，仅用于已有隔离测试环境 |
| auth.enabled=true | /api/v1/mcp | 每次请求携带 Authorization: Bearer 公司JWT或PAT |

开启后旧 /pub/mcp 不注册（404），避免绕过认证。Higress 前缀规则不变，认证版外部地址为 `https://efficient-lab.qiuer.net/k8sctl/api/v1/mcp`；保持网关转发 Authorization，不在客户端和网关之间记录 token。无需修改健康探针 /api/v1/pub/health、/ready。

普通客户端：

```bash
curl -H "Authorization: Bearer ${K8S_CTL_PAT}" \
  https://efficient-lab.qiuer.net/k8sctl/api/v1/auth/me
```

MCP 客户端配置 Streamable HTTP URL 为上述认证版地址，并通过客户端的安全凭据设置发送 Authorization header。401 时更新 JWT 或重新创建 PAT；本服务尚不提供 OAuth/OIDC discovery、授权码登录或自动刷新，不支持只会自动 OAuth 而不能配置 Bearer header 的客户端。

## 验证与源码

- domain/auth：可信身份 context、PAT 模型与端口。
- application/auth：认证、个人归属、PAT 创建/列表/撤销、生命周期限制。
- infrastructure/auth：RS256 验签和 PAT 签发、SQLite 撤销/摘要/最后使用记录。
- interfaces/middleware/auth.go、handler/auth.go、router/router.go：HTTP 认证与路由，身份继续传入 MCP 和业务层。
- cmd/pat：本地管理员首个 PAT 签发；Dockerfile 将工具打包入镜像。
- tests/auth_test.go：真实 RSA 签名、篡改/错误 claims/算法拒绝、PAT 生命周期、持久化、并发限额、REST/MCP 认证、context 身份传递与撤销后已有客户端拒绝。

测试使用临时密钥、临时 SQLite 和本地 MCP HTTP 服务；不使用真实公司 JWT、现有私钥或 Kubernetes 集群，不代表已完成公司网关联调或线上升级。


## 可选的容器诊断桥接

Kubernetes MCP Server v0.1.2 的私有 sidecar 接入、`exec_pod_command`、REST 日志/执行入口及配置/RBAC 见 [接入说明](kubernetes-mcp-integration.md)。默认关闭，启用后在原有工具集上增加一个写工具；已有 `get_pod_logs` 复用，不重复注册上游工具。客户端继续使用 k8s-ctl 的 JWT/PAT 认证入口，不直连 sidecar。

认证模式不再读取旧 control.api-key-file，也不要求 olixops-control Secret；该 volume 允许不存在。切回旧 API-key 模式时，应用启动仍会强制读取配置的密钥文件，缺失或为空则启动失败，不会匿名开放。
