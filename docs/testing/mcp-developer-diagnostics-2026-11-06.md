# 开发人员 MCP 诊断真实验证

时间：2026-11-06T13:25:21.580171+08:00

22 项真实检查全部通过。测试采集通过 MCP，未使用客户端 kubectl 或 SSH；独立 Go SDK 客户端在禁止读取 ~/.kube 和禁止派生子进程的 macOS sandbox 中重跑，同样全部通过。

## 真实链路

PAT 客户端 → 127.0.0.1:18092/api/v1/mcp → 精确用户 grant → 固定诊断动作 → 上游 Kubernetes MCP Server v0.1.2（managed stdio）→ 真实 poc-cluster Pod。上游二进制 SHA256 已核对官方 release asset digest。

目标为 env-vault/env-vault-web-5dcdc4c59-7wt8t 的 env-vault-web 容器。客户端仅配置 PAT；Kubernetes 凭据留在服务端，由与预检相同的 Provider 快照提供。

## 检查明细

| 检查 | 结果 | 导出字节 |
| --- | --- | ---: |
| Only log and diagnostic tools exposed | PASS | — |
| Pod logs via MCP | PASS | 9324 |
| Container environment via MCP | PASS | 1557 |
| Container memory via MCP | PASS | 662 |
| Process enumeration via MCP | PASS | 512 |
| PID 1 status via MCP | PASS | 1334 |
| Approved file nginx.conf via MCP | PASS | 648 |
| Approved file default.conf via MCP | PASS | 1479 |
| Approved file auth-config.json via MCP | PASS | 164 |
| Symlink or non-regular file refused /etc/mtab | PASS | — |
| Symlink or non-regular file refused /var/run/nginx.pid | PASS | — |
| Symlink or non-regular file refused /etc/nginx | PASS | — |
| Other user logs rejected | PASS | — |
| Other user environment rejected | PASS | — |
| ServiceAccount token path rejected | PASS | — |
| Other Pod rejected | PASS | — |
| Arbitrary shell action rejected | PASS | — |
| Client argv rejected | PASS | — |
| Invalid PID rejected | PASS | — |
| Management MCP tool unavailable | PASS | — |
| Management HTTP route hidden /api/v1/cluster/status | PASS | — |
| Management HTTP route hidden /api/v1/services/standard | PASS | — |

## 代码与配置检查

- 全量 go test ./... -count=1 -timeout 120s 通过。
- go vet ./...、git diff --check、Kustomize 本地渲染通过。
- 精确授权、MCP/REST 隔离、Provider 快照和执行边界的定向 go test -race 通过。
- macOS 本机不运行 Linux 专用 reader 单元测试；同一 reader 已在真实 Linux Pod 中执行，读取成功，并拒绝符号链接、符号链接父目录与非普通文件。Linux 专用测试另覆盖 FIFO 和读取上限。

## 导出与测试后状态

- 本地导出目录：/Users/vincent/Desktop/codes.nosync/olixops-k8s-ctl/.local/pod-diagnostics/env-vault-web-mcp-20261106-132521
- 本地归档：/Users/vincent/Desktop/codes.nosync/olixops-k8s-ctl/.local/pod-diagnostics/env-vault-web-mcp-20261106-132521.zip
- 归档不包含 PAT、签名密钥或 kubeconfig；业务输出原文未自动脱敏。
- 两个临时 PAT 已撤销；测试服务保留监听 127.0.0.1:18092，继续使用需由管理员创建匹配 grant 的个人 PAT。
- 原有 8091、18091 实例未改动；本次未修改目标 Pod 或拉取镜像。

## 未覆盖场景

- 未实际部署 sidecar，也未验证最小 RBAC ServiceAccount；生产部署继续使用 sidecar+Namespace RBAC。
- 未接入真实公司 JWT/SSO；本轮真实调用使用 PAT。
- 未提供交互式 shell/TTY 或 C++ 堆分析，也不能承诺超时会杀掉容器内进程。
- Kubernetes 查询 UID 与按名字连接 exec/logs 之间仍无原子 UID precondition。

配置与使用方法见 [开发人员 MCP 诊断](../developer-diagnostics.md)。
