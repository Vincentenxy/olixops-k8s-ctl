# magic-app 2.2.8 动态操作器部署

[values.operator-2.2.8.yaml](values.operator-2.2.8.yaml) 用于 `lucy-chart-release/magic-app:2.2.8`，运行统一的 `cmd/main.go` 并将 `app.mode` 设为 `operator`。publish 保存、解密并注册 kubeconfig，操作器按 clusterId 隔离实例。操作器可部署在管理集群，只需能访问目标 Kubernetes API，不通过 Pod 的 ServiceAccount 操作业务集群。

这是一份本地部署草稿，未绑定平台服务 ID、环境或 Namespace。目录查询未找到现有 k8s-ctl 服务；`service.name: k8s-ctl-operator` 是本包采用的部署名，关联平台服务时按实际 abbreviation 核对。没有保存平台配置或部署集群。

## 构建对应镜像

默认构建就是统一 k8s-ctl 镜像，无需指定 target。运行模式由挂载的 `config.yaml` 中 `app.mode` 决定：

```bash
docker buildx build \
  --platform linux/amd64 \
  --build-arg BASE_IMAGE_REGISTRY=m.daocloud.io/docker.io \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  -t harbor.gtjaqh.net/lucy-dev/k8s-ctl-operator:0.0.1-alpha.10 \
  --push \
  .
```

镜像包含操作器、kubectl、Helm、CA 和 managed 执行程序 `kubernetes-mcp-server v0.1.2`。上游多架构镜像固定 digest，只复制 `/app/kubernetes-mcp-server`，不复制其配置或启动第二个监听端口。路径依据为 [上游 v0.1.2 Dockerfile](https://github.com/containers/kubernetes-mcp-server/blob/v0.1.2/Dockerfile)。无法访问 quay.io 时，先将相同版本/架构镜像复制至内网仓库，用 `--build-arg KUBERNETES_MCP_IMAGE=<内部完整地址>` 指定；`BASE_IMAGE_REGISTRY` 只改变 Go/Alpine 来源。

values 把运行必需及与默认值不同的配置挂载到 `/app/configs/config.yaml`，覆盖镜像内参考配置；省略的字段采用程序默认值，不与镜像内 YAML 合并。没有添加业务环境变量；Chart 自身仍注入 TZ 和 Pod 信息等通用变量。values 省略 securityContext，默认按镜像 USER 10001:10001 运行；不再额外声明 Kubernetes 的 runAsNonRoot 校验。

## config.yaml 内联认证配置

本 values 不把凭据写入 ConfigMap，也不使用 initContainers。平台 JWT/PAT 公钥契约通过部署时的 Secret 或环境变量注入，最终生成 `/app/configs/config.yaml`。不能把私钥、Token 或其他 Secret 填回 `service.configmaps.fileContent`。

```yaml
auth:
  enabled: true
  jwt:
    enabled: true
    public-key: "填写平台 JWT 验签公钥"
    issuer: "填写平台 Token issuer"
    audience: "填写平台 Token audience"
    key-id: "填写平台 Token kid"
  pat:
    enabled: true
```

仓库模板中的公钥和 Token 元数据留空，部署前在私有配置中填写，不能将示例说明文字作为有效值。PEM 公钥保留 BEGIN/END 行和正确缩进。当前用户 JWT/PAT 同时用于注册和业务请求，操作器不保存独立注册控制凭据。默认目标镜像内的参考文件配置会被本 ConfigMap 文件完整覆盖。

### 启动报错排查

`pod-exec requires authentication, Kubernetes writes and an enabled HTTP or writable MCP entry` 表示已开启容器命令能力，但 `auth.enabled`、`kubernetes.write-enabled`、HTTP 或可写 MCP 入口至少有一项不满足要求。操作器应保留 `auth.enabled: true`，并按部署环境开启 `auth.jwt` 和 `auth.pat`。操作器不再使用 `auth.company` 或独立注册控制 Token。

`auth.jwt.public-key` 是平台 JWT 的 RSA 验签公钥。它不是 kubeconfig、JWT Token 或注册凭据，支持完整 PEM 和 Base64 DER。`auth.jwt.issuer` 和 `auth.jwt.audience` 在操作器 JWT 模式下也必须填写。`auth.pat` 只启用平台 PAT 公钥验签。空公钥或缺少 issuer/audience 配置无法完成对应认证类型的初始化。

按当前源码，统一入口根据 `app.mode` 选择运行模式。修改 `config.yaml` 后需要重新构建或重启部署，旧镜像不会自动读取新配置。操作器直接校验平台 Token 的 issuer/audience，不为集群改写 audience；standalone 模式不具备动态注册能力。

## 运行方式与接入

- HTTP、Kubernetes 写入、Shell、终端和文件传输开启；主 MCP 关闭，对外 MCP 由 publish 提供。
- JWT 和 PAT 按部署环境开启，issuer、audience 和 key-id 与平台实际 Token 契约核对。Publish 不改写 Token audience，每个已注册实例继续检查 clusterId 和 runtimeId。
- `pod-exec.authorization-enabled: false` 沿用当前分支由 publish 判断服务权限的方式，不配置静态用户/Namespace grants。Shell 拦截器启用，仍为现有 `allow-all` 占位策略；JWT、用户句柄、Pod UID、文件完整性和目标集群 RBAC 继续生效。
- 不使用 InCluster 身份、不挂载 kubeconfig；注册后使用内存 Provider。Catalog 默认关闭，无 SQLite/PVC；PAT 使用 Setting 的 Redis 记录。临时工作区使用可写 `/tmp` 并按原逻辑清理。HTTP 默认开启，主 MCP 默认关闭，Shell 拦截器默认开启且为 allow-all，kubectl/Helm 与 120 秒命令超时采用程序默认值。
- 单副本，每集群终端总数 128、每用户 16。集群注册上限由 `operator.max-clusters` 配置，默认 16。

Chart 创建监听端口为 8901 的 ClusterIP Service。若使用默认 values，Service 对外端口和容器端口均为 `8901`。同集群 publish 可连接 `http://k8s-ctl-operator.<实际Namespace>.svc.cluster.local:8901`；本地 IDEA 或其他集群需要管理网络、内网代理或临时端口转发，不能假定可访问该 DNS。省略 routes，不创建 Ingress。

publish 配置上述操作器地址。首次调用时由 publish 查询服务和 cluster_info、通过 EnvVault 解密，并在业务权限检查通过后使用当前调用者 Token 注册和调用：

```text
POST /api/v1/operator/clusters/configure
     Authorization: Bearer <当前调用者 JWT 或 PAT>

POST /clusters/{clusterId}/api/v1/cluster/pods/logs
     Authorization: Bearer <当前调用者 JWT 或 PAT>
```

探针采用 Chart 2.2.8 默认的 TCP 存活/就绪检查，端口 `http-8901`，初始延迟 10 秒、超时 3 秒，不额外配置启动探针。该检查只证明监听端口可连接，不证明所有集群可达；HTTP 进程健康接口仍为 `/api/v1/pub/health`。

文件准备返回相对路径，沿用 publish 流式代理。另配 `/k8sctl` 前缀时，代理需去掉前缀并保留 Authorization、Content-Length 和取消传播，不全量缓冲；参考 [代理片段](../platform/examples/nginx.conf)。

## 平台使用与验证边界

平台选择 Chart `lucy-chart-release/magic-app`、版本 **2.2.8**。magic 模式填写 values；magic-v2 可把同一份无变量引用的 YAML 填入 values-desc，本草稿无需新增 envs-desc 变量。保存前读取并合并目标服务原配置，不能直接覆盖其他内容；部署前推送镜像、填写内联注册凭据与平台公钥，并核对 service.name 和实际 Namespace。

离线渲染（已配置相应 Helm 仓库时）：

```bash
helm template k8s-ctl-operator lucy-chart-release/magic-app \
  --version 2.2.8 --namespace "$CTL_NAMESPACE" \
  -f deployments/helm/values.operator-2.2.8.yaml
```

2026-11-15 读取平台 `2963078505 / sim` 源配置和 Chart 上下文，确认其 2.2.8 使用 `service/configmaps/routes` 和 raw 多行正文。未复用 Nginx 业务配置、凭据或路由地址。`0770701122 / sim` 的 values-desc 仅有初始化注释，没有将它当作完整部署依据。

完整缓存包 `magic-app-2.2.8.tgz`（包含同版本 library）全量 Helm 渲染通过，生成 ConfigMap、Service、Deployment。检查 YAML 重复键、raw 正文一致性、探针/端口和配置挂载。该包没有根 values.yaml；附带 values-schema.yaml 第 354 行附近存在引号语法问题，**未通过该说明文件的机器 Schema 校验**。没有修改 Chart，依据为同版本真实模板和渲染结果。认证 Secret/环境变量属于部署覆盖层，不能由这个无凭据模板推断已完成。

操作器状态在单进程内。Chart 默认 RollingUpdate，单副本升级仍可能短暂存在两个 Pod；升级前结束会话并安排暂停流量/停止旧实例，升级后由 publish 重新注册，旧终端和文件引用失效。不能直接横向扩容。共享会话、多副本 HA 和滚动重叠治理仍在后续范围。

本轮 Linux amd64 镜像启动检查通过：加载本 values 配置、非 root 运行、managed v0.1.2 可执行；无启动 kubeconfig 时 health 返回 200；错误注册凭据返回 401、非法注册输入返回 400；注册两个模拟集群后，JWT HTTP 能力查询通过，跨集群 audience 返回 401。HTTP/Shell/终端/文件开关均为开启。模拟地址不连接真实 Kubernetes API，没有执行实际 Pod 命令或文件传输。临时容器和测试凭据已清理。原单集群镜像也重新构建并确认入口未变；`go test ./...`、`go vet ./...`、gofmt 格式检查、`git diff --check` 通过。

默认 quay.io 镜像的一个 40.66MB 层持续无下载进展，本次终止该构建；Daocloud 镜像代理返回 403。为完成运行验证，使用本机已缓存、干净的上游 v0.1.2 源码（提交 `66553fed5c16454e5814b41e331b4357f15806d8`），交叉编译静态 Linux amd64 程序并封装为本地源镜像，通过 `KUBERNETES_MCP_IMAGE` 替换后完整构建成功。这验证了镜像组装、执行程序兼容和启动链路，不代表默认 quay.io 下载已成功。部署环境需能拉取默认上游镜像或准备同版本的内网镜像。

Docker 导出的构建上下文复查为 89 个文件，包含两个 target 的必要入口与无凭据配置，未包含文档、部署 YAML、测试源码或私有运行数据。

当前 values 是无凭据模板，不能直接作为可部署配置。认证需要部署覆盖层提供公司 JWT 公钥、平台 PAT 专用公钥、issuer、audience 和 key-id 的 Secret/环境变量。操作器不连接 Setting Redis；PAT 撤销和业务权限由 Publish/Setting 链路负责。该 values 的 Helm 渲染只验证模板结构，不代表真实认证、Secret 注入、操作器注册或集群访问已通过。临时凭据和容器已清理，仓库中仍为空值模板。

本轮没有保存平台配置、推送镜像或安装业务集群。
