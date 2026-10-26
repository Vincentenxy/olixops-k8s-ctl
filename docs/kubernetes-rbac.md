# 集群内部署的 Kubernetes 权限

当前服务使用 `kubernetes.in-cluster: true`，通过 ServiceAccount `olixops/olixops-k8s-ctl` 连接所在集群。无需把本机 kubeconfig 放到容器里，也无需修改连接配置。ServiceAccount 提供身份，RBAC 规则决定该身份能操作哪些资源；应用内的 MCP 用户权限校验仍是后续功能。

## 基础授权范围

[deployments/rbac.yaml](../deployments/rbac.yaml) 为当前标准服务部署与常见工作负载操作提供权限，文件尚未自动应用到集群：

- 全集群读取 Namespace、StorageClass、GatewayClass 和 TCPRoute；TCPRoute 必须跨 Namespace 查询，才能识别共享 LB 上的既有端口占用。
- 通过独立的 `olixops-k8s-ctl-namespace-creator` ClusterRole/Binding 创建 Namespace，仅授予 `namespaces/create`。
- 在 `wx-test-ns-001`、`wx-test-ns-002` 中管理应用白名单内的工作负载、Service、ConfigMap、PVC、Ingress 和 Gateway API 路由；读取日志、事件和工作负载历史；读取已有的业务 Secret。
- 在 `higress-system` 中读取 Gateway/HTTPRoute/TCPRoute，仅对 `higress-gateway` Service 和 `higress-tcp-gateway` Gateway 授予更新/patch 权限。Kubernetes RBAC 将授权限制到对象，具体端口范围由应用检查；RBAC 不能将对象更新权限限制到某些字段或端口。

业务 ClusterRole 通过 `wx-test-ns-001`、`wx-test-ns-002` 中各自的 RoleBinding 绑定，不会给其他 Namespace 授予业务资源写权限。控制服务自身所在的 `olixops` 未绑定该业务角色。新业务 Namespace 可以通过 MCP `create_namespace` 创建；创建后仍需管理员为其添加同样的 RoleBinding，才能部署业务资源。修改 RoleBinding 的 `metadata.namespace`，subject 的 Namespace 始终保留 `olixops`。

Namespace 创建属于集群级权限，Kubernetes RBAC 无法使用 `resourceNames` 限制 create 请求的名称或前缀。应用仍执行名称校验、Namespace 白名单和 Kubernetes 写开关检查；当前部署白名单为空，表示不限制名称范围。如需限定创建目标，需配置应用白名单或接入准入策略。

基础文件不包含 Namespace 更新/删除、RBAC 修改、节点/CRD 管理、Pod exec 或 Secret 写入。Helm 默认将 release 存储在 Secret 中，需要额外的 Secret list/create/update/delete 等权限；当前基础文件没有授予这些权限，因此不能认为所有 Helm 工具都已可用。新增权限应按实际业务 Namespace 和 Chart 资源补充。

读取 Secret 和创建 Pod/工作负载均属于受信任控制权限；仅在允许该服务管理的业务 Namespace 中创建 RoleBinding。该配置不会把集群授予任意调用者的身份问题一起解决。

## 应用与验证

先检查文件内的业务 Namespace 和 Higress 对象名称。当前集群已有 `wx-test-ns-001`、`wx-test-ns-002`、`higress-system`，服务账号也已存在。使用有权限创建这些 Role/Binding 的管理员 kubeconfig 执行：

```bash
kubectl apply --dry-run=server -f deployments/rbac.yaml
kubectl apply -f deployments/rbac.yaml
```

这里管理员的 kubeconfig 只用于在外部执行 kubectl 配置 RBAC，服务本身继续使用 Pod 的 ServiceAccount。RBAC 生效无需重启 Pod、重建镜像或重新生成 token。

按账号检查授权结果：

```bash
kubectl auth can-i list namespaces \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
kubectl auth can-i create namespaces \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
kubectl auth can-i create statefulsets.apps -n wx-test-ns-001 \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
kubectl auth can-i list tcproutes.gateway.networking.k8s.io --all-namespaces \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
kubectl auth can-i update services/higress-gateway -n higress-system \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
```

应用后的预期结果为 `yes`。以下越界检查预期为 `no`，但已有其他 RoleBinding 的权限会叠加，RBAC 中没有 deny 规则：

```bash
kubectl auth can-i delete namespaces \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
kubectl auth can-i patch namespaces \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
kubectl auth can-i create statefulsets.apps -n olixops \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
kubectl auth can-i update services/another-service -n higress-system \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
kubectl auth can-i create clusterrolebindings.rbac.authorization.k8s.io \
  --as=system:serviceaccount:olixops:olixops-k8s-ctl
```

检查者还需要 impersonate 该身份的权限；普通用户运行 `--as` 可能因缺少 impersonate 权限而失败。

最后通过 MCP 调用 `cluster_status`、`list_namespaces`、`list_resources(namespace="wx-test-ns-001")`，确认真实访问；调用 `sync_gateway_ports` 会读取共享入口并更新 SQLite 台账。这里只做授权，不会自动部署业务服务。

如需在应用层也收紧 Namespace 范围，可在部署 ConfigMap 中设置 `kubernetes.allowed-namespaces: [wx-test-ns-001, wx-test-ns-002, higress-system]`，应用配置修改后需重启；基础 RBAC 本身已经限制 Kubernetes 写操作范围。后续增加业务 Namespace 时，应用白名单与 RoleBinding 都要相应调整。

原理与对象作用域见 [Kubernetes RBAC 官方文档](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)。

标准服务部署要求显式指定 Namespace；执行部署时读取该 Namespace，只有 NotFound 才尝试创建。已有的 namespace-creator ClusterRole 提供创建权限，但不会自动给新 Namespace 绑定 workloads 角色。新 Namespace 内业务资源权限须由管理员配置；自动创建不绕过 allowed-namespaces、写开关或 Authorizer。失败后保留 Namespace，不随实例删除。
