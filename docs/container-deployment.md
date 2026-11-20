# Docker 打包与 Kubernetes 部署

> 2026-11-06 认证更新：已实现可选公司 JWT/PAT 认证。`auth.enabled=true` 时业务接口需要 Bearer，MCP 改为 `/api/v1/mcp`（Higress 下加 `/k8sctl`），旧 `/api/v1/pub/mcp` 不注册。本文原公开入口/API key 或待实现认证描述仅适用于旧兼容模式；资源级授权仍待实现。配置、令牌接口与迁移见 [认证接入说明](authentication.md)。

默认镜像是 publish 动态操作器，包含 kubectl、Helm 3、managed Kubernetes MCP 执行程序和 CA 证书。publish 负责对外 MCP、服务权限和 kubeconfig 注册，操作器提供认证 HTTP 调用。镜像以 UID/GID 10001 运行，CLI 临时文件使用 `/tmp`，Helm 缓存使用用户主目录，这些位置需要可写。单集群版本的 SQLite 内嵌在应用中，数据库写入 `/data/olixops.db`；默认操作器不启用 Catalog/PAT 数据库。

## 构建镜像

本文默认命令构建统一 k8s-ctl 镜像。通过 `configs/config.yaml` 的 `app.mode` 选择 `operator` 或 `standalone`，对应 Chart 2.2.8 values 和运行方式见 [平台操作器 Helm 部署](../deployments/helm/README.md)。镜像从固定 digest 的上游 v0.1.2 镜像复制 managed 执行程序，不依赖在业务集群安装 sidecar。

在项目根目录执行。以下命令生成本地测试标签；部署清单中的 Harbor 镜像地址需使用实际推送的版本：

```bash
docker buildx build --platform linux/amd64 \
  -t olixops-k8s-ctl:0.2.0 --load .
```

ARM 节点使用 `--platform linux/arm64`。Mac Apple Silicon 上准备给 x86 集群部署时，需要显式指定 `linux/amd64`。构建依赖 Docker BuildKit，Go 模块和编译结果使用构建缓存。

构建 amd64 镜像并直接推送到 Harbor：

```bash
docker login harbor.gtjaqh.net

docker buildx build \
  --platform linux/amd64 \
  --build-arg BASE_IMAGE_REGISTRY=m.daocloud.io/docker.io \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  -t harbor.gtjaqh.net/lucy-dev/k8s-ctl:0.0.1-alpha.2 \
  --push \
  .
```

`BASE_IMAGE_REGISTRY` 只改变构建和运行基础镜像的来源，推送目标由 `-t` 指定。`GOPROXY` 只用于构建阶段。

构建并推送多架构镜像；将示例仓库地址替换为实际仓库：

```bash
docker login registry.example.com
docker buildx build --platform linux/amd64,linux/arm64 \
  -t registry.example.com/olixops/olixops-k8s-ctl:0.2.0 --push .
```

如果当前 builder 提示不支持多架构输出，先创建专用 builder（只需一次）：

```bash
docker buildx create --name olixops-builder --driver docker-container --use
```

仅构建单架构再推送也可以：

```bash
docker tag olixops-k8s-ctl:0.2.0 registry.example.com/olixops/olixops-k8s-ctl:0.2.0
docker push registry.example.com/olixops/olixops-k8s-ctl:0.2.0
```

Dockerfile 默认使用 Docker Hub 的 `golang:1.26-alpine` 构建、`alpine:3.22` 运行。通过 `--build-arg BASE_IMAGE_REGISTRY=...` 统一覆盖基础镜像仓库前缀；也可通过 `--build-arg GO_IMAGE=... --build-arg RUNTIME_IMAGE=...` 分别指定完整镜像地址，优先于仓库前缀。运行基础镜像需提供兼容 Alpine 的 apk 仓库以及 kubectl、Helm 3 包。

镜像只复制 Go 源码、依赖清单和唯一的 `configs/config.yaml`。真实 kubeconfig、API key、SQLite 文件不应放入构建内容；依赖下载认证也不要使用 Docker ARG 传递。

## 构建内容与缓存

`.dockerignore` 使用白名单，默认排除所有内容，仅允许以下文件进入 Docker 构建上下文：

- `go.mod`、`go.sum`、Docker 构建定义
- `cmd/main.go`
- `internal/**/*.go`、`pkg/**/*.go`，排除 `*_test.go`
- `configs/config.yaml` 作为所有运行模式的运行时默认配置

Agent 生成的文档、报告、Skills、截图、PPT/PDF、README、AGENTS、测试记录、部署清单、测试源码及本地数据不会发送到构建器。开发配置、`.env`、kubeconfig 和本地 Secret 也不在白名单中。以后增加嵌入资源或新的构建入口时，应同时更新白名单及 Dockerfile，避免遗漏编译依赖。

Go 源码仅在构建阶段编译；最终镜像不携带源码、Go 编译器或文档。统一镜像包含操作器、单集群服务、PAT 验证和上游 managed 执行程序，运行模式由 `app.mode` 选择。生产打包不运行测试，测试在独立流程执行。

缓存按以下方式组织：

1. 先复制 `go.mod`、`go.sum` 并下载模块。仅修改业务源码不会使这一步失效。
2. 使用 BuildKit 的 `/go/pkg/mod` 和 `/root/.cache/go-build` 缓存，复用原有缓存路径；模块未变化时避免重复下载，源码变化时复用未变化包的编译结果。
3. 统一入口一次 `go build` 同时覆盖两种运行模式。共用依赖、源码阶段与 Go 缓存，不编译重复入口。
4. 独立 `runtime-base` 阶段安装运行依赖，与源码编译无关，BuildKit 可并行准备；源码修改不会重新安装工具。
5. 文档和测试文件不进入上下文，它们的变更不会使生产源码复制/编译层失效。

日常只构建实际节点所需的一个架构，且复用同一个 buildx builder。不要常规使用 `--no-cache`、`--pull` 或清理 builder 缓存。第一次构建仍需下载依赖并编译 Kubernetes/SQLite 等包；代理、机器性能和缓存状态会影响实际耗时，不承诺固定秒数。

本地构建并加载镜像：

```bash
docker buildx build --platform linux/amd64 \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  -t k8s-ctl:dev --load .
```

在原生 ARM 节点使用 `--platform linux/arm64`。CI 使用临时 builder 时，可以额外配置远程层缓存：

```bash
docker buildx build --platform linux/amd64 \
  --cache-from type=registry,ref=registry.example.com/olixops/k8s-ctl:buildcache \
  --cache-to type=registry,ref=registry.example.com/olixops/k8s-ctl:buildcache,mode=max \
  -t registry.example.com/olixops/k8s-ctl:dev --push .
```

替换为实际仓库，使用支持 registry cache exporter 的 buildx builder（如 docker-container driver）。远程层缓存和可执行步骤里的 cache mount 是两种缓存；导出层缓存不会自动导出 cache mount 的内容。需要跨流水线持续保留 Go 编译缓存时，优先复用持久化 builder。

### 构建验证（2026-11-15）

本次使用 Docker 实际导出上下文核对：87 个必要文件、410792 字节，生产 Go 源码无遗漏，没有多余文档、测试、本地配置或其他入口。Linux amd64 两个二进制构建通过，Go 编译步骤耗时约 51.5 秒。

Docker Hub 获取 Alpine 3.22 元数据时连接被拒绝，改用 `--build-arg RUNTIME_IMAGE=m.daocloud.io/docker.io/library/alpine:3.22` 后，完整镜像成功构建为本地 `k8s-ctl:docker-audit`。未推送远程。

运行检查通过：UID/GID 10001、`/data` 可写、两个二进制与默认配置存在；`/app` 只有这三个文件，没有源码/文档；kubectl 客户端与 Helm 3 可执行。未连接真实 Kubernetes 集群。

同一 builder、相同参数和相同内容重复完整构建，13 个步骤命中缓存，本机耗时 1.23 秒。该数字仅为本次热缓存验证，不能代表首次构建或源码改动后的耗时。`go test ./...`、`go vet ./...` 通过；Dockerfile 和 ignore 文件的 diff 空白检查通过。

### 默认操作器验证（2026-11-16）

统一 Dockerfile 实际构建 Linux amd64 镜像成功，检查入口为 `/app/k8s-ctl`，用户为 `10001:10001`。使用临时注册凭据与 RSA 公钥挂载配置，将 `app.mode` 设为 `operator`，以 `--network none` 启动容器；无需启动 kubeconfig，日志为 `platform operator started`，进程健康接口返回 `{"code":0,"data":{"operator":true},"msg":""}`。

统一镜像将 `app.mode` 改为 `standalone` 后可运行独立单集群服务。`go test ./...`、`go vet ./...`、`git diff --check` 通过。

本次构建使用缓存的 Go/Alpine 层，`KUBERNETES_MCP_IMAGE` 指定此前准备的本地同版本 `k8sctl-upstream:local-v0.1.2`，没有验证默认 quay.io 镜像的网络下载。测试容器和临时凭据已清理；未连接真实集群、推送镜像或更新线上部署。新默认值只对重新构建的镜像生效。

## 单集群兼容部署

以下 catalog.yaml、InCluster、SQLite/PVC 和旧 API key 说明适用于 `app.mode: standalone`。统一镜像无需切换 Docker target，修改挂载的 `config.yaml` 后重启即可。publish 动态操作器请使用 [Chart 2.2.8 部署包](../deployments/helm/README.md)。

## 部署前准备

`deployments/catalog.yaml` 包含 Namespace、ServiceAccount、ConfigMap、PVC、单副本 Deployment、ClusterIP Service 和 Higress Ingress。部署前核对：

- Namespace `olixops` 和专用 ServiceAccount `olixops-k8s-ctl` 已在清单中；Secret 和 RBAC 需要在应用启动前准备，首次安装时可以先创建这两个基础资源。
- ServiceAccount 对应目标 Namespace/资源的 Kubernetes RBAC；清单创建身份，不包含 Role/ClusterRole/Binding，也不会授予 cluster-admin。缺少授权时进程仍可能启动，但集群操作会返回 Forbidden。

  可核对并单独应用 [基础 RBAC](../deployments/rbac.yaml)，具体范围与操作见 [Kubernetes 权限说明](kubernetes-rbac.md)。该文件的业务 Namespace 是 `wx-test-ns-001`、`wx-test-ns-002`，控制服务位于 `olixops`。
- 旧 API-key 兼容模式要求 Secret `olixops-control`，包含 `api-key`。应用监听非回环地址时会检查这个配置，REST 请求通过 `X-Olixops-Control-Key` 携带。JWT/PAT 模式不读取此旧密钥，改为准备认证密钥；volume 允许缺失，旧模式仍由启动校验拒绝缺失密钥。
- 可用的默认 StorageClass，或在 PVC 中显式填写 `storageClassName`。底层存储须支持 SQLite 的文件锁；不要让多个进程同时使用这份数据库。
- 私有镜像仓库需要的 `imagePullSecrets`，按仓库情况加入 Pod 配置。
- IngressClass `higress`，以及入口域名的 DNS 和所需 TLS 证书。部署清单使用域名 `efficient-lab.qiuer.net`，可以修改 `spec.rules[].host`；如果开启示例 TLS 部分，`tls.hosts` 也要一起修改。

先准备 Namespace、ServiceAccount（命令可重复执行）：

```bash
kubectl create namespace olixops --dry-run=client -o yaml | kubectl apply -f -
kubectl -n olixops create serviceaccount olixops-k8s-ctl --dry-run=client -o yaml | kubectl apply -f -
```

创建应用引用的 Secret；该步骤必须在首次启动前完成：

```bash
bash scripts/prepare-control-secret.sh
```

脚本使用 kubectl 和 OpenSSL，只在 Secret 不存在时创建。随机 key 保存在 Git 忽略的 `.local/control-api-key`，权限为 0600，不输出内容；重复执行不会覆盖已有 Secret。Pod 出现 `FailedMount: secret "olixops-control" not found` 时，执行此脚本补齐同 Namespace 的 Secret，旧版本 kubelet 会自动重试挂载，无需删除 Pod。当前 YAML 的该 Secret 为可选挂载；旧 API-key 模式缺失时应用会启动失败，仍需补建并重启。

如果已有自己的 key，可以改为从受保护的本地文件创建 Secret，不将真实 key 写到命令参数或 Git 中：

```bash
kubectl -n olixops create secret generic olixops-control \
  --from-file=api-key=/absolute/path/to/api-key
```

文件内容是 API key 本身，读取时会移除末尾的换行；Secret 以文件挂载到 `/app/secrets/control/api-key`，非 root 进程通过 fsGroup 读取，不使用环境变量。已有资源不需要重复创建。RBAC 需允许实际使用的业务资源；标准服务部署涉及 StatefulSet、Service、ConfigMap、PVC、TCPRoute，查询还涉及 Pod、Secret、Namespace 和 StorageClass，维护共享入口还涉及 Higress 所在 Namespace 的 Service 和 Gateway。端口核对会跨 Namespace 列出 TCPRoute。Helm 还需要其 release 存储和目标 Chart 声明的资源权限。

## 应用部署清单

先修改 `deployments/catalog.yaml` 中的镜像地址，确认 Ingress host、Secret 和 RBAC 已准备好，再执行：

```bash
kubectl apply -f deployments/catalog.yaml
kubectl -n olixops rollout status deployment/olixops-k8s-ctl --timeout=180s
kubectl -n olixops get pods,pvc,svc
kubectl -n olixops get ingress olixops-k8s-ctl
kubectl -n olixops logs deployment/olixops-k8s-ctl --tail=100
```

SQLite 当前只支持一个运行副本，因此保留 `replicas: 1` 和 `strategy: Recreate`。新 PVC 只会初始化默认模板及端口池；原来本机 SQLite 的配置和端口记录不会自动迁移。迁移请先停止原进程并执行一致性备份，避免两个服务同时协调同一个入口。

应用读取 `/app/configs/config.yaml`。集群中该文件由 `olixops-k8s-ctl-config` ConfigMap 的 `config.yaml` 挂载提供，直接修改 `deployments/catalog.yaml` 中 `data.config.yaml` 即可设置运行模式、监听地址、MCP、Kubernetes 写开关、数据库路径、Higress 和端口范围。Deployment 不设置运行时环境变量。

`control.api-key` 保持空，`control.api-key-file` 指向 Secret 挂载文件。读取失败或空文件会阻止启动，错误信息不包含文件内容；两种 key 来源不能同时配置。应用只在启动时读取配置和 key，修改 ConfigMap 或 Secret 后执行：

```bash
kubectl apply -f deployments/catalog.yaml
kubectl -n olixops rollout restart deployment/olixops-k8s-ctl
kubectl -n olixops rollout status deployment/olixops-k8s-ctl --timeout=180s
```

本次新增文件读取能力，需要重新构建并使用包含该代码的镜像；旧镜像不识别 `api-key-file`。

清单保留现有读写开关；本次没有新增应用认证或授权逻辑。Secret 中的 API key 保护 REST 控制路由，公开的 `/api/v1/pub/mcp` 不使用该 key。Higress Ingress 会将这个公开路由一同暴露在 `/k8sctl/api/v1/pub/mcp`。

## Higress 的 /k8sctl 访问前缀

服务自身路由仍是 `/api/v1/...`。Ingress 匹配 `/k8sctl(/|$)(.*)` 并重写为 `/$2`，去掉外部前缀；不需要修改 Go 路由或容器健康检查。Ingress 和后端 Service 均在 `olixops` Namespace，后端引用 Service 的 `http` 端口（8901）。

外部访问路径示例：

- 健康检查：`http://efficient-lab.qiuer.net/k8sctl/api/v1/pub/health`。
- 就绪检查：`http://efficient-lab.qiuer.net/k8sctl/api/v1/pub/ready`。
- MCP：`http://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp`。
- REST 模板列表：`http://efficient-lab.qiuer.net/k8sctl/api/v1/templates`，携带现有的 `X-Olixops-Control-Key`。

`/k8sctl` 本身会转发到 `/`，当前应用没有首页，返回 404 是预期行为。前缀边界匹配不会接管 `/k8sctl-other/...`。数据库服务的 30000–30200 TCP 端口池与 HTTP 访问前缀是不同用途；控制服务复用 Higress 已有的 HTTP/HTTPS 入口，不额外创建 LoadBalancer。

注解使用 Higress 支持的 `use-regex`、`rewrite-target`，请求超时为 180 秒，高于应用默认写操作的 120 秒，并将网关重试次数设为 0。当前 MCP 是无状态 Streamable HTTP、JSON 响应，使用普通 HTTP 路由转发即可。参见 [Higress 注解说明](https://higress.io/en/docs/latest/user/annotation/)。

HTTPS 需要该域名已在网关配置有效证书，或在 Ingress 中启用注释里的 `tls` 并提供同 Namespace 的 TLS Secret。DNS 应指向 Higress 的 LB；以下检查快照中的 LB IP 是 `10.77.75.40`，部署时以实际查询为准。

验证外部路由（如果使用 HTTPS，将协议替换为 https）：

```bash
curl -fsS http://efficient-lab.qiuer.net/k8sctl/api/v1/pub/health
codex mcp add k8s-ctl-higress --url http://efficient-lab.qiuer.net/k8sctl/api/v1/pub/mcp
```

DNS 尚未配置时，可以先使用 LB 地址和 Host 头验证 HTTP 路由：

```bash
curl -fsS -H 'Host: efficient-lab.qiuer.net' \
  http://10.77.75.40/k8sctl/api/v1/pub/health
```

## 集群检查快照（2026-10-26）

只读查询确认 IngressClass `higress` 存在；`local-path` 是默认 StorageClass；共享 `higress-system/higress-gateway` 的 LB IP 为 `10.77.75.40`，已暴露 80/443。已有 Higress Ingress 在 `efficient.qiuer.net` 使用相同的正则前缀和重写注解。

当时 `olixops` Namespace、`olixops-k8s-ctl` ServiceAccount、`olixops-control` Secret 均返回 NotFound；Namespace 和 ServiceAccount 现已加入待部署清单，Secret 和 RBAC 仍需准备。镜像仓库地址、镜像推送、私有仓库凭据、域名 TLS 也需要在实际安装前确认。该快照反映首次安装前状态；后续安装修复与验证结果见下文。

## 检查服务并连接 MCP

```bash
kubectl -n olixops port-forward service/olixops-k8s-ctl 8902:8901
```

另开终端检查：

```bash
curl -fsS http://127.0.0.1:8902/api/v1/pub/health
curl -fsS http://127.0.0.1:8902/api/v1/pub/ready
codex mcp add k8s-ctl-cluster --url http://127.0.0.1:8902/api/v1/pub/mcp
```

使用本地 8902 以便与开发进程的 8901 区分；port-forward 终端需要保持运行。健康检查只证明 HTTP 进程可用；通过 MCP 的 `cluster_status`、`list_service_templates`、`list_storage_classes` 再确认集群连接、目录与读取权限，真实部署前调用 `sync_gateway_ports` 核对入口占用。

## 本次验证（2026-10-26）

`go test ./...`、`go vet ./...`、gofmt 检查、容器配置与 Kubernetes YAML 解析检查通过；Linux amd64/arm64 的 `CGO_ENABLED=0` 二进制编译通过。新增 ConfigMap 和 Secret 文件挂载后，7 类资源通过 `kubectl create --dry-run=client --validate=true -f deployments/catalog.yaml`；验证了健康检查、MCP、模板列表的前缀重写及前缀边界示例。已通过真实 Higress 的 HTTP 路由验收；本机 HTTPS 检查因证书签发链不受信任而未通过，未更改证书配置。

首次直接访问 Docker Hub 构建超时；后续通过 `BASE_IMAGE_REGISTRY=m.daocloud.io/docker.io` 和 `GOPROXY=https://goproxy.cn,direct` 成功构建 Linux amd64 镜像并推送 `harbor.gtjaqh.net/lucy-dev/k8s-ctl:0.0.1-alpha.8`。容器烟测验证非 root 启动、挂载 Secret key 文件、健康检查和带 key 的模板查询。

现场修复：缺失 `olixops-control` Secret 导致 FailedMount，补建后发现旧 `alpha.1` 镜像不支持 `api-key-file`；已将 Deployment 更新到 `alpha.8`，Pod 达到 1/1 Ready、0 次重启。经 LB 和 `Host: efficient-lab.qiuer.net` 验证 `/k8sctl/api/v1/pub/health` 返回成功，MCP 握手、48 个工具发现和 `list_service_templates` 只读调用通过。该验证未执行 Redis/PG 部署。


## 可选的容器诊断桥接

Kubernetes MCP Server v0.1.2 的私有 sidecar 接入、`exec_pod_command`、REST 日志/执行入口及配置/RBAC 见 [接入说明](kubernetes-mcp-integration.md)。默认关闭，启用后在原有工具集上增加一个写工具；已有 `get_pod_logs` 复用，不重复注册上游工具。客户端继续使用 k8s-ctl 的 JWT/PAT 认证入口，不直连 sidecar。


开发人员使用的两个诊断工具、按用户精确授权、managed 本地 MCP 验证和文件读取新增边界，见 [开发人员 MCP 诊断](developer-diagnostics.md)。该模式与完整管理工具集分开部署。


### 下载地址与网关前缀

主 MCP 文件下载采用“准备引用 + 认证 HTTP 流”的方式。基础配置及 catalog.yaml 的 ConfigMap 已设置 `server.public-url: https://efficient-lab.qiuer.net/k8sctl`；更换域名时同步修改为实际地址，含 Higress 路径前缀。该配置不改变服务监听路由，沿用现有 `/k8sctl` 重写。不要将容器内部 Service 地址作为外部下载地址。下载依然携带有效 PAT/JWT，不生成公开下载链接。

当前下载成功响应为原始二进制流，无固定大小上限；应用未设置总下载超时，但 Higress/客户端/集群连接可能中断。网关缓冲、长连接与大文件并发的专项调优留待第二阶段验证，见 [待办](roadmap.md)。
