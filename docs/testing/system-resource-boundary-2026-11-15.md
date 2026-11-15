# 开发者对集群系统资源的操作边界审计

日期：2026-11-15。只读审计，不执行系统资源变更，不修改真实 RBAC。

## 结论

目前不能声明“所有系统资源写入已被统一硬禁止”。publish 的八个 k8sctl 工具绑定服务、环境和真实 Pod 归属，不能直接指定 Namespace 来修改 ingress-nginx；但下层应用没有不可绕过的系统 Namespace 写入禁令。资源类型白名单能挡住通用 Manifest 中的 ClusterRole、Node 等类型，不能单独阻止系统 Namespace 下白名单类型的 Deployment、ConfigMap 等资源。开发环境开放策略不等于允许操作基础设施。

## 已核实证据

| 检查 | 结果 | 结论范围 |
| --- | --- | --- |
| publish ServiceTargetResolver + ServiceObservationService | 集群、Namespace、Release 由服务数据确定；Pod 必须属于该服务 | 不能从普通服务直接指定另一个系统 Pod；系统组件若被登记为可操作服务仍需额外保护 |
| 开发者 publish PAT 直接访问下层 operator/capabilities | HTTP 401 | 该 PAT 不能直接作为可信后台 JWT 使用 |
| 内部可信后台 JWT，仅调用 kubectl/validate，目标 ingress-nginx 下 ConfigMap | HTTP 200，通过本地校验 | 仅证明应用策略缺少系统 Namespace 保护；没有 apply，也不证明 Kubernetes RBAC 允许写入 |
| 同样仅 validate，目标 kube-system 下 ConfigMap | HTTP 200 | 同上 |
| 同样仅 validate，ClusterRole | HTTP 403 | 资源类型白名单拒绝该类型 |
| 同样仅 validate，普通 Namespace 内 privileged/hostPID/hostNetwork Pod | HTTP 200 | Manifest 本地校验没有独立的特权工作负载约束；没有创建 Pod、没有拉镜像 |
| 用户 10121993 经 publish Shell 在服务 2963078505 仿真 Pod 内执行 SelfSubjectAccessReview | 对 ingress-nginx Deployment patch、kube-system ConfigMap patch、ClusterRole patch、Node patch 均 allowed=false | 仅证明当前业务 Pod 的 ServiceAccount 对四项请求无权限；不代表其他 Pod、其他动作或后台 kubeconfig 的权限 |

业务 Pod 挂载了 ServiceAccount Token。权限查询在容器内使用 Token，仅将权限判定结果带回；未导出 Token。SelfSubjectAccessReview 不修改目标资源。

## 仍需处理的边界

- publish 的服务目标解析和应用写入口需要独立的基础设施保护，不能由 dev/test 的开放策略或普通业务 Grant 覆盖
- 下层 `internal/application/authorization.go` 当前默认 DeferredAuthorizer 为占位放行；普通 Manifest 仅核对类型与 Namespace 一致性，不判断是否基础设施
- `internal/infrastructure/kubernetes/pod_exec.go` 的 hostNetwork/hostPID/hostIPC、hostPath 和 privileged Pod 限制，目前和可关闭的执行授权开关绑定；应将基础设施安全边界与个人业务授权分开
- 需要配合实际后台 kubeconfig、业务 Pod ServiceAccount 的最小 RBAC 和集群工作负载准入策略，不能只依赖平台参数校验
- 原有 Catalog/Higress 端口管理会受控修改部分网关系统资源，后续保护设计要区分平台管理能力与开发者直接系统操作，避免简单禁止所有系统 Namespace 导致合法管理流程失效

本记录是审计结论，本轮未实现上述系统保护改造。MCP 环境允许列表只控制目标环境入口，不替代系统资源边界。
