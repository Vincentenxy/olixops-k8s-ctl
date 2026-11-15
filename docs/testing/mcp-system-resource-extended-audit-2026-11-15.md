# 环境常量与系统资源边界扩展检查

版本：0.1.0-alpha.1  
日期：2026-11-15

## 检查范围与结果

在 [112 个 MCP 工具审计](mcp-system-resource-audit-2026-11-15.md) 基础上，继续检查当前 IDEA publish 项目的权限表达式、Mapper XML、中文环境标签、普通 HTTP、旧 Helm 和 WebSocket 入口，以及 k8s-ctl 路由认证、动态操作器初始化、文件传输、执行适配器与 kubeconfig Provider。

新发现并修复环境常量问题 3 处（2 个 Java 文件、1 个 Mapper XML）。另外发现 2 组配置条件下的安全入口缺口；当前仍没有统一、不可关闭的系统资源保护。本次没有调整认证路由和授权开关行为，没有修改集群资源。

## 已修复的环境值

| 位置（publish 相对路径） | 原问题 | 修复 |
| --- | --- | --- |
| src/main/java/net/gtjaqh/pubdevops/permission/PublishPermissionConfiguration.java | 权限 DSL 字符串内部使用单引号写死四个环境；上一轮完整双引号搜索无法发现 | 环境访问、快速回滚、网络步骤策略的环境值由 EnvsEnum 格式化生成，现有权限码及策略组合保持不变 |
| src/main/java/net/gtjaqh/pubdevops/comm/EnvsCnConstants.java | 中文环境名在枚举外重复维护 | 复用枚举 getLabel()，保留原 Map 和查询方法契约 |
| src/main/resources/mappers/InventoryInfo.xml | queryByAuditVersion 的 SQL 写死生产环境 | MyBatis bind 从 EnvsEnum.PROD 获取值，使用参数绑定，不拼接 SQL |

本次搜索覆盖完整双引号值、字符串内单引号、中文环境标签、大小写、XML 和环境正则。现存单引号环境值还可见于注释掉的历史权限注解，不在当前执行链中，本次保留历史注释。接口字段名、提示文案、YAML 配置值、Git 分支/存储路径和测试协议样本不是业务环境判断，不机械替换。

## 新发现 A：API key 模式未保护公开 MCP

优先级：高；特定配置组合下存在未认证写入口。

必要条件：`auth.enabled=false`、`mcp.enabled=true`、`mcp.write-enabled=true`，并开启相应 Kubernetes 写能力。单独配置 `control.api-key` 不能保护这条 MCP 路由。

源码链：

- `internal/interfaces/router/router.go`：认证关闭时在 `/api/v1/pub/mcp` 注册 MCP；只有 `protected` 分组使用 `middleware.ControlKey`。
- `internal/interfaces/mcp/server.go`：写工具仅根据 WriteEnabled 注册，没有在工具回调中补 API key 校验。
- `internal/application/authorization.go`、`internal/domain/authorization.go`：默认 Authorizer 为占位放行，不要求认证主体。
- `internal/infrastructure/config/config.go`：非回环监听要求 API key，但没有阻止这个 MCP 写工具组合。

### 本地安全复现

使用纯本地 Gin 路由和 httptest，不配置 Kubernetes 客户端、命令 Runner 或真实凭据。注入专用审计 Authorizer，在写请求到达应用授权入口时立即拒绝，确保不会进入执行适配器。探针不访问真实集群。

| 检查 | 结果 |
| --- | --- |
| 不携带 API key 调普通 HTTP validate | HTTP 401 |
| 不携带 API key 调公开 MCP tools/list | HTTP 200，共 29 个当前配置工具 |
| 写工具注册 | apply_manifest、delete_resource、helm_upgrade、update_namespace_metadata 均可见 |
| 不携带 API key 调 MCP 本地 YAML 校验 | HTTP 200、valid=true |
| apply 请求到达应用层写授权入口 | 是，且上下文没有已认证 Principal |
| 审计 Authorizer 阻断写请求 | 是，工具错误；未调用任何 Kubernetes 或本地命令执行器 |

复现文件保留在忽略目录 `.local/platform-integration/extended-boundary-audit-20261115/`，结果只记录状态和布尔值，不包含真实凭据。

这是认证入口缺口，不只是系统 Namespace 分类缺失。当前 publish 动态操作器强制公司 JWT 认证，**不符合上述触发条件**；不能据此断言当前 publish 的所有操作都是匿名开放的。独立部署和其他配置组合仍须处理。

建议后续将可写 MCP 放在明确认证的入口，或在 API key 模式对整个 MCP 传输执行同等认证；同时增加启动配置校验。不要只隐藏 tools/list，因为调用者仍可以直接 tools/call。若保留公开只读兼容入口，必须从路由和工具注册两方面保证其不可执行写操作。

## 新发现 B：publish 旧入口依赖服务详情鉴权开关

优先级：高；开启保护时有目标校验，关闭保护时存在原始目标调用路径。

### 旧 HTTP Helm

`controller/HelmController.java` 的 install、upgrade、uninstall 在 `requireBusinessEntry()` 中仅在 `authorization.active()` 为 true 时拒绝原始接口。`PublishAuthorizationService.active()` 使用 `AuthzCenterProperties.detailActive()`，即权限中心总开关与服务详情开关同时开启。

关闭这些开关后，请求可以继续交给旧 Helm Service，携带原始集群、Namespace 和发布参数。这不代表无需登录，也未验证外部 Helm Wrapper 的最终 RBAC；它说明系统目标保护不应依赖个人/服务授权开关。

### 旧 WebSocket Shell

`WsPodController` → `WsPodServiceImpl` → `HelmWsClientService.send()` → `PublishWebSocketAuthorizationService.authorize()`。

后者在 `!authorization.active()` 时直接返回原始消息，跳过工作负载归属校验与目标绑定；开启时才调用 `PublishWorkloadAuthorizationService.requireRawTarget()` 并对输入绑定用户及目标。WebSocket 自身仍有身份认证，不应误报为未认证入口。

旧 WebSocket 使用自己的 Helm Wrapper 执行链，不经过 k8s-ctl 的 Pod 安全检查或 Shell 拦截器。因此仅收紧新 k8sctl MCP 工具，不能覆盖旧入口。

建议把始终生效的基础设施目标检查置于共享执行边界，并覆盖旧 HTTP/WS 执行链；个人/服务授权仍可按业务需求灰度，业务开发 Shell 保持开放。

## 其他路径核对

- 下层普通 HTTP 与 MCP 共用应用服务，所以前次发现的系统 Namespace 写保护缺失也影响 HTTP；协议转换不会自动添加目标保护。
- 动态操作器初始化要求独立引导凭据，按集群 audience 校验后续公司 JWT；未发现开发者 MCP 能直接调用初始化入口的工具。不是所有有效公司 JWT 都应获得引导权限。
- 内存 kubeconfig Provider 拒绝本机路径型凭据、Exec/AuthProvider 插件、TokenFile 和 impersonation，不能通过上层 kubeconfig 插件直接在操作器宿主机执行命令。
- 文件流 HTTP 使用认证分组，引用绑定用户、Pod UID、有效期和单次消费；实际传输时重新检查目标，不是拿到 URL 就匿名下载。
- 文件路径校验拒绝直接使用 /proc、/sys、/dev 和常见 ServiceAccount Secret 路径；这是词法检查。下载使用 tar 的跟随符号链接选项，不能据此宣称链接目标也被这些路径规则隔离。当前任意 Shell 已开放，所以该路径规则本身也不是容器凭据读取的完整隔离边界。
- kubectl/Helm 使用参数数组，容器上传固定脚本在容器内执行、路径独立传参；未发现新增的调用方命令在控制器本机 shell 拼接执行路径。

以上均为源码核查结论，不代表完成外部 Helm Wrapper、集群 RBAC、准入控制和网络隔离的真实全面验收。

## 验证结果

### publish

新增 H2 集成测试 `InventoryInfoMapperSqlTest`，实际加载 Mapper 并查询四种环境和不同审计版本的数据，验证只返回目标版本的生产记录。数据库仅在本机内存中存在。

将快速回滚权限测试扩为四个 EnvsEnum 参数：错误环境权限码拒绝、匹配权限码允许、无服务范围拒绝。

最终相关回归：44 个测试类、460 项测试，其中 456 通过、4 项既有 Disabled 跳过、0 失败、0 错误。覆盖 MCP、环境策略、目标解析、工作负载/WS 授权、配置、文件代理和 Mapper。日志：本机 `/tmp/publish-environment-extended-regression-20261115.log`。

与此前一致，未纳入已有 SQL 条数预期失败的 KnowledgeSkillContractTest、DeploySqlBundleTest；不宣称全项目全部测试通过。修改文件的 diff 空白检查通过。

### k8s-ctl

- `go test ./...`：通过，其中部分结果来自 Go 缓存
- `go vet ./...`：通过
- 本地入口探针：证实 API key 仅保护普通 HTTP、未保护上述公开 MCP 配置；所有写请求在审计 Authorizer 处主动停止

没有修改 Go 生产源码或提交新认证策略。本次审计结果应作为后续系统资源保护设计与回归用例的输入。
