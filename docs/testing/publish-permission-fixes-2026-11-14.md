# publish 权限测试后修复与复测

日期：2026-11-14。本记录接续 [权限实测报告](publish-permissions-10121993-2026-11-14.md)，保留初次发现问题的原始结果，不重写历史清单。

## 本轮确认范围

1. 修复 publish HTTP 业务异常状态，按实际错误码返回
2. k8s-ctl 代理明确不支持灵雀云；publish 原有 MCP 的 LQY 适配继续保留
3. 开发环境 Shell 维持开放策略，不新增用户 Grant 限制
4. 原报告第 2 项“能力声明与执行不一致”、第 3 项“MCP 错误格式”仅解释，本轮未修改其策略或返回契约

## HTTP 状态修复

新建 publish 本地 `PublishBusinessExceptionHandler`，限定到 publish HTTP Controller，并优先于旧公共异常处理器处理 `CommException`。原公共处理器对全部这类异常固定返回 HTTP 500；新处理器在业务错误码为 400–599 时使用同一 HTTP 状态，正文 `code/msg/data` 保留原值。-1、600 等非 HTTP 业务码或空值回退 HTTP 500，不把错误转换为 2xx/3xx。

修复没有改动其他模块使用的 efficient-common 公共异常处理器，也没有将 MCP 工具错误转换成普通 HTTP 异常。MCP 外层传输成功与工具执行成功仍是两件事，失败继续按 SDK `isError` 处理。

## 灵雀云能力边界

`ServiceTargetResolver.resolve()` 在解析集群、Namespace 与执行后端前检查真实服务的 `deployTarget`：

- `k8s`：继续既有代理流程
- `lqy`：返回 `[unsupported_target] 灵雀云服务不支持 k8s-ctl 代理操作，请使用 publish 原有的灵雀云工具`
- 缺失或未知：返回 `unsupported_target`，不猜测为原生 K8s

判断只在 `service/k8sctl` 代理路径中，未添加到 publish 原有工具、公共服务或全局认证过滤器。原生 K8s 仍需已有控制器接入；未接入返回 backend_not_configured 是正常配置错误，不再将其当成 Grant 验收失败。

## 真实联调

使用原有合法 PAT，用户 10121993 / 员工号 121993。HTTP 连接本地 IDEA publish 9001；k8s-ctl 由 publish 经 HTTP 调用本地操作器 19091。未改权限、授权关系、业务配置或部署。仅执行固定 printf，没有新建文件或会话。

| 场景 | 入口 | HTTP | 业务结果 | Request ID |
| --- | --- | --- | --- | --- |
| 无权限查询基本信息 | /api/v1/service/query/id | 403 | code=403 | authz-10121993-731850d10c1e4f |
| 无权限读取生产日志 | /api/v2/service/status/log/stream?serviceId=3899597151&curEnv=prod&podName=permission-probe-not-a-real-pod&container=gxj-adm-api&tailLines=1 | 403 | code=403 | authz-10121993-3a14b577c9164d |
| 有权限查询基本信息 | /api/v1/service/query/id | 200 | code=0 | authz-10121993-3a44921c66484b |
| k8sctl 日志：灵雀云 | k8sctl-get-service-logs | 200 | unsupported_target，明确不支持 | authz-10121993-3c23139d4cf443 |
| k8sctl Shell：灵雀云 | k8sctl-exec-service-command | 200 | unsupported_target，明确不支持 | authz-10121993-47933f3084f349 |
| k8sctl 打开终端：灵雀云 | k8sctl-open-service-terminal | 200 | unsupported_target，明确不支持 | authz-10121993-95106e1f6e244e |
| k8sctl 上传准备：灵雀云 | k8sctl-prepare-service-file-upload | 200 | unsupported_target，明确不支持 | authz-10121993-ee822773d2d54b |
| k8sctl 下载准备：灵雀云 | k8sctl-prepare-service-file-download | 200 | unsupported_target，明确不支持 | authz-10121993-6014acbc1b7440 |
| publish 自有配置上下文工具：灵雀云服务 | query-service-config-context | 200 | code=0 | authz-10121993-40ba51f72a7745 |
| k8sctl Shell：原生 K8s 正常执行 | k8sctl-exec-service-command | 200 | outcome=completed，输出正确 | authz-10121993-03ac15b9ecc043 |

10 项真实检查通过。5 个灵雀云初始代理工具均在 publish 返回 unsupported_target；按 Request ID 核对，下层操作器没有收到对应请求。终端 write/read/close 使用已有会话引用，不能为不支持的灵雀云目标创建初始会话，本轮未伪造会话引用。

publish 自有 query-service-config-context 对同一灵雀云服务成功；这仅验证原有工具未被代理范围判断拦截，不据此声称已实测所有原生灵雀云部署/运行工具。

## 回归测试

针对性首轮：23 项通过。随后运行 HTTP 异常、k8sctl HTTP/终端/文件/配置注册、MCP 身份与运维文档传输、SDK HTTP 适配，以及服务权限/运行态授权的相关现有测试，最终回归 **150 项通过，0 失败、0 错误、0 跳过**。

新增 MVC 测试覆盖：常用 4xx/5xx 与扩展 599、非 HTTP 业务码回退、空错误码、成功响应兼容、与旧全局处理器共存时的优先级。新增目标解析测试覆盖灵雀云明确不支持、未解析执行后端、未知或缺失部署目标拒绝。现有 openEnvironmentOperationsDoNotContactAuthzEvenWhenCenterIsUnavailable 用例继续验证 dev/test 环境操作（含 TERMINAL）保持开放策略，身份和实际目标检查仍有效。

```bash
mvn '-Dtest=PublishBusinessExceptionHandlerTest,ServiceTargetResolverTest,ContainerHttpFlowTest,CtlFileControllerTest,LogHttpFlowTest,CtlReferencesTest,CtlOperatorBootstrapTest,CtlHttpTest,PublishAuthorizationServiceTest,PublishAuthorizationEnforcementTest,RuntimeStatusAuthorizationTest,ServiceOperationsDocumentMcpIntegrationTest,McpAuthenticationIntegrationTest,SdkVariableHttpIntegrationTest' test
```

本次 Java 修改位于 `/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api`；下层 Go 项目仅补文档。运行实例已通过真实复测；未自动提交或推送代码。

## 原第 2、3 项的解释

**第 2 项是能力提示不一致。** 例如某服务的“运维文档”是服务共用的一份，不属于 dev/sim 中任意单独环境；用户对它没有权限。能力查询在 dev/test 下却显示 DOCUMENT_READ=true，实际读取仍返回未授权。问题是页面/Agent 会以为可以读，实际点击才发现不允许；开发环境 Shell 默认允许属于已确认策略，与这个共享能力显示错误分开处理。

**第 3 项是 publish 的 MCP 错误格式不统一。** ServiceConfigTools.save 直接调用业务服务，授权异常交给 SDK 变成 isError=true 加错误文字；运维文档等工具会适配为带 code=403 的结构化错误。两种都拒绝了执行，后一种更便于客户端按业务码分类。这里不是 k8s-ctl 的权限拦截失效，也不应通过改变 MCP 外层 HTTP 状态去修复；本轮保留现有契约，后续需要时再统一。
