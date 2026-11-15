# publish / 本地操作器联调报告

日期：2026-11-13。范围：本轮新增八个 k8sctl MCP 工具与本地多集群运行入口。

## 结论

代码链路已完成，Go 与 Java 编译无错误，接入相关 48 项 Java 自动化测试全部通过，Go 全量测试、vet 和指定并发/race 检查通过。真实环境已完成用户认证、服务解析、EnvVault 解密、配置注册、下层能力查询、Pod 选择和容器日志读取。

**持续终端成功操作、文件真实上传下载以及安装/卸载生命周期尚未完成真实验收。** 目标服务三个容器在查询时均处于 waiting；所选容器累积重启 18,327 次。用户本次要求遇到环境问题先记录，优先完成链路和编译，因此没有修复业务容器、安装/卸载服务或拉取镜像。

## 环境与定位

- publish：IDEA 当前工作目录 `/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api`，端口 9001，MCP `/pubops/mcp`
- k8s-ctl：本地 `cmd/operator`，回环端口 19091；业务集群未部署控制器
- 联调身份：10121993，复用已配置的 publish PAT，不伪造身份 Header
- 服务：8824195328；环境 sim；集群 gdl-dev-lucy；Namespace devops-efficient-test1
- Chart 记录：lucy-chart-release/magic-app 2.2.7；查询到 Deployment Updating、0 个就绪副本，三个 Pod 的目标容器均 waiting

首次 EnvVault 解密失败曾定位为本地 PAT 用户记录缺失；用户调整后重新验证，解密和本地注册已成功。密文、Token、连接串和证书未写入本报告或对话。

## 自动化验证

| 检查 | 结果 |
| --- | --- |
| `gofmt`（新增/修改 Go 文件） | 完成 |
| `go test ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/interfaces/operator ./internal/infrastructure/kubeconfig ./internal/application/shell/...` | 通过 |
| Maven 离线 `-DskipTests compile` | 通过 |
| Java 接入定向测试 | 48 项通过，0 failures / 0 errors |

Java 测试组成：McpAuthenticationIntegrationTest 15、CryptoOptServiceTest 5、ContainerHttpFlowTest 15、CtlFileControllerTest 3、CtlHttpTest 3、CtlOperatorBootstrapTest 2、CtlReferencesTest 2、LogHttpFlowTest 1、ServiceTargetResolverTest 2。

真实 Boot 4 MCP 传输测试验证八个工具的发现、参数绑定、JWT/PAT 身份、结构化输出与 Jackson 2/3 适配。本地 HTTP 模拟控制器验证单次命令结果、TTY 控制字符透传、cursor/lostBytes、会话归属、runtime/UID 变化、文件二进制及零字节、Content-Length、SHA-256、截断/断流与禁止重试；这部分不是实际 Kubernetes 终端/文件验收。

Go 新增检查覆盖：自包含 kubeconfig、拒绝凭据插件与文件路径、拷贝隔离、取消、注册认证、未知字段、集群路径隔离、同配置复用 runtime、容量限制、配置替换期间在途请求保留以及关闭清理。

补充验证：模拟命令响应延迟 11 秒仍成功返回，修复 OkHttp 默认 10 秒读取超时早于命令预算的问题；HTTP 读写和总调用预算统一为 35 秒，命令本身仍限制为 1–30 秒。

回归发现并修复：Servlet 实现可忽略 `setHeader(name, null)`，错误 JSON 可能继承旧 Content-Length/Content-Disposition。现在仅在响应未提交时重置并恢复非文件 Header；流已提交后的异常继续中断传输，不追加 JSON。

## 真实 MCP 逐项结果

| 工具 | 真实调用结果 | 验收状态 |
| --- | --- | --- |
| k8sctl-get-service-logs | 多 Pod 返回 selectionRequired；指定 Pod 后 code=0，实际返回日志 | 通过 |
| k8sctl-exec-service-command | 默认资格缺失时 403；精确 UID 测试授权后，容器未运行，返回 conflict / 409 | 失败边界通过，命令成功执行未验收 |
| k8sctl-open-service-terminal | 容器未运行，返回 conflict / 409 | 失败边界通过，成功打开未验收 |
| k8sctl-prepare-service-file-download | 容器未运行，返回 conflict / 409 | 失败边界通过，文件流未验收 |
| k8sctl-prepare-service-file-upload | 容器未运行，返回 conflict / 409 | 失败边界通过，文件流未验收 |
| k8sctl-write-service-terminal | 无有效终端时，测试引用返回 not_found | 失败边界通过，真实输入未验收 |
| k8sctl-read-service-terminal | 无有效终端时，测试引用返回 not_found | 失败边界通过，真实输出未验收 |
| k8sctl-close-service-terminal | 无有效终端时，测试引用返回 not_found | 失败边界通过，真实关闭未验收 |

原有 `query-service-deploy-status` 也已实际调用成功。真实操作器认证边界也已验证：正确集群 audience 返回 200，错误集群 audience 返回 401；用户 JWT 无权注册凭据，注册控制凭据也不能作为用户 JWT 操作集群，两者均返回 401。本报告没有宣称验证现有全部 publish 工具或下层所有业务工具。

## 清理与后续复测

本地临时精确 UID grant 已移除，操作器恢复无 per-user grants 配置；业务 Pod、部署、PVC/PV 未修改，没有在容器中创建测试文件。临时完整进程环境快照已删除；运行私钥和控制凭据仍仅位于忽略目录并限制本地权限，以便继续启动服务。

后续先使目标容器处于 running，再通过现有 Pod `k8s-ctl/exec-enabled=true` 标记或管理员精确 UID grant 满足执行资格。不要为了验收删除现有目标检查。然后依次复测 exec、终端输入/读取/TTY 中断/关闭、文件二进制与零字节往返/单次引用重放，最后按用户原始授权测试安装中、安装后及卸载后的行为，并记录最终部署状态。持久卷清理不在本轮授权中。

本轮未自动提交或推送 Git。
