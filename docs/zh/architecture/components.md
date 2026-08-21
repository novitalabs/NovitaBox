# 组件职责

## boxapi

HTTP 控制面入口，默认监听 `127.0.0.1:8080`。

- 注册兼容 API 和 NovitaBox 原生 API。
- 校验 JSON 请求并统一返回错误。
- 维护 template build 等控制面元数据。
- 调用 boxlet gRPC 执行节点操作。
- 暴露 runtime capability、image、template 和 sandbox API。

## boxlet

节点级 agent，默认监听 `127.0.0.1:8081`。

- 创建和恢复 sandbox 本地目录。
- 启动或连接每 sandbox 的 boxshim。
- 分配 network slot 和 host access IP。
- 创建 network namespace、veth、tap、路由和 NAT。
- 构建 Firecracker snapshot template 和 gVisor directory-rootfs template。
- 管理 template、image、snapshot 和 sandbox metadata。
- 将 balloon 请求转发到支持该能力的 boxshim。

## boxshim

每个 sandbox 一个 runtime supervisor，使用 sandbox 目录中的 Unix socket 提供 RPC。

- 保持为 runtime 父进程。
- 隔离 Firecracker、gVisor 等实现差异。
- 负责 create/start/pause/resume/stop/reboot/kill/status。
- Firecracker driver 负责 API socket、jailer、snapshot、metrics 和 balloon。
- gVisor driver 负责 OCI bundle、runsc state、网络 namespace 和 CDI/GPU 注入。

## boxd

sandbox 内 agent。Firecracker 中运行在 guest VM；gVisor 中作为 OCI init 进程运行。

- health check
- 命令执行
- interactive shell / PTY
- process/connect 兼容接口
- guest 内服务入口

template build 会把 host 上的 `boxd` 注入 artifact。Firecracker 使用只读 agent drive；gVisor 直接将文件放入 directory rootfs。

## boxproxy

数据面代理，默认监听 `127.0.0.1:8082`。

- 转发 exec 和 shell。
- 支持 WebSocket 升级。
- 解析 sandbox ID 或端口域名。
- 从数据库读取 network slot，通过 host access IP 连接 boxd。

## boxctl

本地管理 CLI。生命周期和 artifact 请求走 boxapi，exec/shell 走 boxproxy。

## SQLite

boxapi 和 boxlet 使用 SQLite 保存 template、build、image、sandbox 和 network slot 等元数据。数据库记录不是 artifact 本体；删除或迁移时要同时考虑数据库和 `$ROOT` 文件。

