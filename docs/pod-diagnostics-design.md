# Pod 日志与命令执行设计

日期：2026-11-06。根据最新决定改为复用 Kubernetes MCP Server；旧版“直接使用 client-go 自研 exec、返回独立 stdout/stderr/exitCode”的建议已被替代。

当前实现与部署步骤以 [Kubernetes MCP Server 接入](kubernetes-mcp-integration.md) 为准：

- 保留已有 get_pod_logs，不重复开发上游 pods_log。
- 新增 exec_pod_command，复用 containers/kubernetes-mcp-server v0.1.2 的 pods_exec。
- k8s-ctl 负责 JWT/PAT、作用范围、固定动作与授权扩展点，私有 sidecar 负责 Kubernetes exec 协议。
- 使用固定动作，不开放任意 shell 字符串、stdin 或 TTY；默认关闭，显式开启需要认证、两个写开关、业务 Namespace、Pod 授权标签和 RBAC。
- 上游只返回文本及工具错误标志，不能声称支持独立输出流或退出码。超时结果不确定，不能盲目重试。
- 工作负载日志聚合、交互式终端、沙箱执行器和完整逐用户授权留待后续需求，不在本次重复建设。

代码和本地模拟协议测试已提供；真实上游镜像及集群联调需要在部署环境完成，本次未拉取镜像或执行真实容器命令。
