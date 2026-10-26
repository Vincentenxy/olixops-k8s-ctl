# Redis Operator 测试记录

2026-10-26，在 `wx-test-ns-002` 部署了由集群现有 Opstree Redis Operator v0.26.0 管理的单实例 Redis，通过共享 Higress LB `10.77.75.40:30109` 访问。没有安装新的 Operator，也没有为 Redis 创建独立 LoadBalancer。

当前状态：已按要求清理测试实例、Service、ConfigMap 和 TCPRoute，移除共享入口上的 `30109` 监听与 Service 端口项，并通过 MCP 释放端口，台账状态为 `free`。两个测试 PVC（`redis-b09ee1fa-data`、`redis-redis-0`）及对应 PV 均已删除；Namespace 与密码 Secret 保留。下文资源表和验证结果为清理前的部署记录，原访问地址现已停用。

## 实际资源

| 资源 | 名称 / 配置 |
| --- | --- |
| Namespace | `wx-test-ns-002` |
| Redis CR | `redis.redis.opstreelabs.in/v1beta2`，`Redis/redis` |
| Operator 生成的 StatefulSet / Pod | `redis` / `redis-0` |
| Redis 镜像 | `quay.io/opstree/redis:v7.4.6` |
| 镜像策略 / 节点 | `Never` / `k8s-poc-worker4` |
| 密码引用 | 本 Namespace 的 `redis-credentials` Secret，键 `password` |
| 持久化 | `redis-redis-0` PVC，`local-path`，1Gi；AOF 开启 |
| 配置 | `redis-operator-config` ConfigMap |
| 后端 Service | `redis:6379`，ClusterIP |
| TCPRoute | `redis-30109` → `higress-system/higress-tcp-gateway`，listener `olixops-tcp-30109` |
| 端口台账 | `30109`，`external`，无冲突，指向 `wx-test-ns-002/redis-30109` |

使用 [部署清单](../deployments/redis-operator-demo.yaml)。镜像已缓存于指定节点，`Never` 保证不会尝试拉取；如果缓存被清理或节点不可用，Pod 将无法启动，不会自动拉取镜像或切换到无缓存节点。这是单实例测试配置，没有 Sentinel、主从切换或 Redis Cluster。

## 验证结果

- Pod `1/1 Running`，StatefulSet `readyReplicas=1`，PVC `Bound`。
- StatefulSet 的 controller ownerReference 指向 `Redis/redis`，确认由 Operator 管理。
- TCPRoute 的 `Accepted` 与 `ResolvedRefs` 均为 `True`。
- Pod 没有 init container，Redis 容器的 `imagePullPolicy=Never`；事件为 `Container image ... already present on machine`。
- 从当前机器经 `10.77.75.40:30109` 验证：未认证 PING 返回 `NOAUTH`，正确密码 AUTH 返回 `OK`，PING 返回 `PONG`。
- `CONFIG GET appendonly` 返回 `yes`。未进行重启后的数据恢复、故障切换或其他网络位置的连接测试。

可以使用客户端交互式输入密码，不把真实密码放到命令参数中：

```bash
redis-cli -h 10.77.75.40 -p 30109 --askpass PING
```

密码未提交 Git，保存在 Kubernetes Secret；本次生成的本地副本位于项目 Git 忽略目录 `.local/redis-30109/password`，权限 0600。

## 重新应用的前提

清单用于当前测试环境，不是完整的 Operator 安装包。重新应用前确认 Namespace、Secret、已有 Operator/CRD、节点缓存镜像、StorageClass，以及共享 Service 上的 `30109` 和 Gateway listener 均已存在。先同步端口台账，确认端口空闲或仍由此同一个实例占用，避免覆盖别人的入口。

```bash
kubectl apply --dry-run=server -f deployments/redis-operator-demo.yaml
kubectl apply -f deployments/redis-operator-demo.yaml
kubectl -n wx-test-ns-002 rollout status statefulset/redis --timeout=60s
kubectl -n wx-test-ns-002 get pods,pvc,svc,tcproute
```

清单不修改共用 LB Service 或 Gateway；测试时 `30109` 的共享暴露由先前标准部署创建。清理后该监听和 Service 端口项已移除，重新部署时需要先准备这些共享入口资源。

## 与 MCP / 标准目录的关系

Namespace 创建、原标准实例部署与清理、端口同步均通过已部署的 MCP 完成。当前通用 MCP 清单接口的资源白名单尚不包含 Redis CR，标准模板也只支持 StatefulSet，因此本次 Redis CR 通过本机 kubectl 应用，没有扩大通用工具白名单或控制服务 RBAC。

原失败实例 `b09ee1fa-16a6-4357-a036-68055a3b2b45` 已通过 `delete_service_instance` 完成清理，部署记录为 `deleted`。删除流程当时按既有策略保留原 PVC `redis-b09ee1fa-data`，新 Operator 实例使用独立 PVC，没有进行数据迁移。两块 PVC 及对应 PV 随后均已按要求单独删除。

部署时，新 TCPRoute 经 `sync_gateway_ports` 识别为 `external`，表示由标准目录之外的资源占用，当时该端口不会再分配给其他实例。Operator 实例配置保存在 Kubernetes CR 中，不能误用旧的 deploymentId 调用 `deploy_service` 或 `verify_service_access`；目前也不能通过标准目录 API 删除 Operator 实例。本次清理使用 kubectl 删除 Redis CR，并通过 MCP 删除其路由、配置、PVC，完成端口同步与释放。
