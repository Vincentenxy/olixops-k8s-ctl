# 真实可写 Pod 文件传输验证（2026-11-06）

用户授权临时部署一个可写目标，验证上传/下载，并将本地 codex-models.json 上传到 /app。

## 测试目标

- Namespace：env-vault。
- Pod：k8s-ctl-file-transfer-demo，容器 file-transfer。
- Pod UID：65cd0ceb-990b-4ceb-ad52-ad71f4706cd4。
- 节点：k8s-poc-worker2，与已有 env-vault-2 相同。
- 复用节点已有镜像 harbor.gtjaqh.net/lucy-dev/env-vault:0.0.1-alpha.10，imagePullPolicy=Never；未拉取或安装镜像。
- 根文件系统保持只读；/app 挂载 256Mi emptyDir，runAsUser/runAsGroup/fsGroup=10001，可写。
- 关闭 ServiceAccount token 自动挂载，不挂载业务配置或 Secret；容器运行 sleep，无需 Service、Ingress 或 LB 端口。
- restartPolicy=Never、activeDeadlineSeconds=7200，最长运行 2 小时；Pod 对象不会自动删除，删除 Pod 后临时文件不保留。

所有集群查询、创建、Shell 与文件传输均经 k8s-ctl MCP/HTTP；没有在开发人员本机使用 kubectl/SSH 下载或上传文件。部署使用原主 MCP，文件验证使用更新代码构建的独立回环实例 18094，PAT 与 Provider 配置沿用当前服务。新增测试 Pod 的精确 UID grants 已写入 configs/config.yaml；原 8091 进程未重启，因此它仍需重新编译并重启才能加载修复和测试授权。

## 发现并修复的真实问题

首轮上传接口返回 513,264 字节和传输流哈希，但回读的实际文件只有 166,744 字节，说明仅验证发送流与 exec 成功不足以确认远端完整接收。该次截断文件只存在于新建测试 Pod，已通过 MCP 删除后重试。

修复方式：上传必须声明准确 Content-Length，未声明长度的 chunked 请求在开始前拒绝。远端 head 按长度读取，服务端在收到完整 HTTP 请求后保留 stdin，等待远端完成确认，避免提前 EOF 与缓冲数据发生竞态。远端核对 wc 字节数并重新读取临时文件计算 SHA-256，服务端将其与接收流比较，一致才进入不覆盖的发布阶段。远端输出最多捕获 128 字节，不缓冲整个文件。输入错误或长度不符取消传输，不发布目标。

增加回归测试：远端命令报告成功但摘要错误时必须拒绝且不发布文件；未知长度请求拒绝且不消费引用。容器需已有 head（支持 -c）、wc、sha256sum，以及原有 mkdir/rmdir/rm/ln/sh；不自动安装工具。

## 修复后的实际结果

| 文件 | 字节数 | 结果 |
| --- | ---: | --- |
| codex-models.json | 513,264 | 上传成功，远端大小/哈希确认，MCP 下载回本地后逐字节内容一致 |
| 二进制测试文件（含 NUL 和非 UTF-8） | 2,097,408 | 上传、远端校验及下载回读成功；重复上传拒绝，原内容保持；测试后通过 MCP 删除 |

codex-models.json 的本地原文件、容器文件、下载文件 SHA-256 一致：

```text
93dd349ceb0f8787ab173264d44481a2c9a1f6fb598ac5b49981489a134cb023
```

容器实际状态：

```text
-rw------- 1 envvault envvault 513264 /app/codex-models.json
```

未发现遗留的 .k8s-ctl-upload-* 临时目录。原文件与测试 Pod 保留供用户继续验证；下载回读文件为忽略目录 .local/manual-mcp-test/codex-models-roundtrip.json，权限 0600。测试清单保存在同目录 file-transfer-demo.json。

## 范围

本次验证了单用户、单实例、约 0.5MiB/2MiB 文件，不代表完成 GB 级文件、并发、断点续传或持久性验收。Higress 未参与此回环链路。后续事项仍见 [开发待办](../roadmap.md)。

最终检查：gofmt 已执行；go test ./...、go vet ./...、上传/下载集成测试 race 与 git diff --check 均通过。独立 18094 测试实例在测试后关闭，8091 保持原状。
