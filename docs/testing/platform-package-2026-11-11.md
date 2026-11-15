# 平台接入部署包本地验证（2026-11-11）

分支：`feat-developer-ctl`。交付入口：[deployments/platform](../../deployments/platform/README.md)。本轮新增部署、RBAC、配置、脚本与测试，没有修改 Kubernetes 业务用例或上层 Java 项目。

## 已完成的检查

| 检查 | 结果 | 范围 |
| --- | --- | --- |
| `kubectl kustomize deployments/platform` | 通过，16 个资源 | 默认资源/Helm HTTP 控制器，不连接 API Server |
| `kubectl kustomize deployments/platform/full` | 通过，22 个资源 | 包含私有执行 sidecar、业务 exec RBAC、SQLite PVC 和 Higress RBAC |
| `go test ./tests -run TestPlatformDeployment -count=1` | 通过 | 原始及渲染后配置加载、开关、clusterId/audience、范围与挂载契约 |
| `python3 -m unittest discover -s deployments/platform/scripts -p 'test_*.py'` | 4 项通过 | 拒绝 HTTP 重定向、清理前 UID 变化拒绝删除、apply 结果未知不重试/删除、正常路径只修改新测试 ConfigMap |
| `bash -n deployments/platform/scripts/prepare-auth.sh` | 通过 | Shell 语法，没有执行脚本里的集群写入 |
| `python3 -m py_compile deployments/platform/scripts/smoke.py` | 通过 | Python 语法 |
| `python3 deployments/platform/scripts/smoke.py --help` | 通过 | CLI 参数及默认只读模式 |
| `gofmt -w tests/platform_deployment_test.go` | 完成 | 本轮新增 Go 测试，未改 Go 业务代码 |
| `go test ./... -count=1` | 全部通过 | 包含既有 HTTP-only、认证、Shell、终端、文件流模拟集成测试；tests 包约 16.5 秒 |
| `go vet ./...` | 通过 | 全项目静态检查 |
| `git diff --check` | 通过 | 现有跟踪文件差异无空白错误；新增脚本/清单另行本地检查 |

Go 部署契约测试重点：

- HTTP 可独立启用，主 MCP 关闭时完整配置仍能加载 Shell/终端/文件能力；PAT 关闭，可信公司 JWT 开启。
- InCluster Provider，不设置 kubeconfig 文件路径；全量配置包含业务与 Higress 范围，exec 范围不含 Higress。
- 控制器位于独立 Namespace，Service 选择器匹配 Pod，Service 只暴露 HTTP，不暴露内部 MCP 8082。
- 平台公钥 Secret 有显式挂载；生成的 ConfigMap 引用可解析，完整入口的配置替换实际生效。
- RoleBinding 引用包内对应角色，ServiceAccount 身份与 Namespace 一致，不绑定 cluster-admin。
- Namespace 更新/删除限定 resourceNames；Helm release 所需 Secret create 权限存在；完整入口包含持久卷。
- 不在清单内提交 Secret 或默认创建外部 Ingress。

本地测试修正了两个部署问题：完整入口引用父入口导致的 Kustomize 递归，以及集群内 HTTP `server.public-url` 被配置校验拒绝。最终使用 sibling base/full 结构；public-url 留空返回相对文件路径，由上层转换。

## 未执行的验收

本轮**没有连接真实集群，没有 apply/RBAC 变更，没有拉取镜像、安装工具、读取真实凭据或操作现有业务数据**。公钥准备和 HTTP 检查脚本未在真实环境执行。Nginx 示例只是供已有 server 引入的配置片段，未启动 Nginx 验证。

这些检查不能替代 API Server 的版本/schema/admission 校验、实际镜像启动、Secret 文件读取、ServiceAccount RBAC、PVC 挂载、Higress/CRD 兼容或上下层真实 JWT 验证。部署清单保留待替换镜像、公钥文件和环境配置，不能原样 apply 后就视为可用。

下一步由操作者准备实际镜像和平台公钥，核对集群/Namespace/RBAC，选择一个入口部署。先运行默认只读 smoke，再显式使用 `--write-test` 测试隔离 ConfigMap；随后联调平台真实身份、动作授权、终端与文件代理。完整入口的 Catalog 启用前需确认旧实例不再协调同一 Higress 端口池，并完成台账/卷迁移方案。
