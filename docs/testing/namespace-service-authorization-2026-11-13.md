# Namespace 名单取消测试记录

日期：2026-11-13。上层为 IDEA 项目 publish-devops-api，下层为当前 k8s-ctl。完整行为见 [调整清单](../namespace-interception-audit.md)。

## 自动化结果

| 检查 | 结果 |
| --- | --- |
| 变更 Go 文件 gofmt / git diff --check | 通过 |
| `go test ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go run ./cmd/api-docs` | 通过，更新 OpenAPI；工具名称与业务路径保持不变 |
| `go test -race ./internal/interfaces/operator ./internal/application/shell/... ./tests -run 'TestRegistry\|TestExecNamespaceAuthorization\|TestOperatorHTTPWithoutMainMCP\|TestTerminalAndBinaryDownloadThroughMainMCP'` | 通过；本次正则选中注册器、Namespace 授权及 HTTP/MCP 终端文件集成用例，shell 子包未命中用例 |
| publish `mvn -o -q '-Dtest=Ctl*Test,ServiceTargetResolverTest,ContainerHttpFlowTest,LogHttpFlowTest,McpAuthenticationIntegrationTest,CryptoOptServiceTest' test` | 50 项通过，0 失败、0 错误、0 跳过；包含代码编译 |
| `go build ./cmd/operator`（输出至本地私有目录） | 通过，新二进制已替换运行 |

回归覆盖无 Namespace 名单注册、兼容旧字段、250 项旧名单不触发个数限制、名单变更不替换 runtime、跨 Namespace 初始化缓存复用、通用资源/MCP Namespace 列表不再按名单过滤、Catalog 预检不受旧名单限制、exec 与 HTTP 终端/文件不要求名单。现有参数、YAML 目标一致性、认证、Authorizer 拒绝、用户会话归属、文件完整性和 UID 相关测试继续执行。

## 本地运行与真实链路

已删除本地 publish 覆盖配置与操作器私有配置中的旧 allowed-namespaces 字段，不输出或变更凭据。更新并重启本轮此前启动的操作器，`127.0.0.1:19091/api/v1/pub/health` 返回 code=0。

通过真实 publish `/pubops/mcp` 调用 `k8sctl-get-service-logs`：

- 当前认证用户：10121993
- 输入：serviceId=2963078505，env=sim，tailLines=5；没有 Namespace 参数
- 平台解析：clusterId=gdl-dev-lucy，Namespace=setting-efficient
- 返回：isError=false、code=0、msg=success；日志文本 600 字符，truncated=false
- 已确认去除静态名单后，服务解析、凭据注册、跨 Namespace 资源查询和日志读取链路可用

日志原文只保存在本地私有结果文件，不复制到仓库。该结果不代表 Shell 和文件写入已经真实验收；本轮未修改业务 Pod、标签、grants、容器文件或安装/卸载服务。Pod 执行资格等保留检查仍可能拒绝真实 Shell/文件请求。
