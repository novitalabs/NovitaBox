# NovitaBox 中文文档

这套文档以当前仓库代码为准，覆盖安装、运行时、artifact、网络、HTTP/RPC、CLI、GPU、Firecracker Balloon 和故障排查。

## 快速入口

- [完整使用手册](usage.md)：从安装到 template、sandbox、image、SDK 和排错的完整流程。
- [安装与服务检查](quick-start/install.md)：Linux、macOS + Lima、运行时依赖和安装变量。
- [HTTP API](api/http.md)：当前实际注册的 HTTP 路由、请求体和示例。
- [RPC API](api/rpc.md)：boxapi、boxlet、boxshim 之间的 gRPC 边界。
- [boxctl CLI](cli/README.md)：template、sandbox、image、runtime 和 balloon 命令。
- [架构概览](architecture/overview.md)：组件关系和数据流。
- [运行时与能力矩阵](architecture/runtime.md)：Firecracker、gVisor、Cloud Hypervisor 的实现差异。
- [网络模型](architecture/network.md)：network namespace、veth、tap、路由和 NAT。
- [Artifact 模型](architecture/artifact.md)：Image、Template、Snapshot 的区别。
- [文件布局](architecture/file-layout.md)：`$ROOT` 下各类文件的位置和用途。
- [生命周期](architecture/lifecycle.md)：sandbox 状态和各 runtime 支持情况。
- [安全模型](architecture/security.md)：jailer、gVisor、GPU 和本地控制面边界。
- [故障排查](troubleshooting.md)：template build、网络、KVM、GPU、balloon 和代理问题。

## 当前实现重点

- Firecracker MicroVM、full snapshot、pause/resume 和 jailer。
- Firecracker virtio-balloon：动态回收目标、统计信息和 free-page hinting。
- gVisor `runsc` directory-rootfs runtime。
- gVisor NVIDIA GPU：`nvproxy`、CDI、设备和 driver library 注入。
- template/image/sandbox artifact 管理。
- 每 sandbox 独立 network namespace 和 host access IP。
- `boxapi`、`boxlet`、`boxshim`、`boxd`、`boxproxy`、`boxctl` 完整链路。

## 文档约定

- 默认安装根目录按安装脚本使用 `/data/novitabox`；程序自身未通过安装脚本启动时，配置默认值是 `/var/lib/novitabox`。
- `tpl-...`、`img-...`、`sbx-...` 都是示例 ID，需要替换为真实返回值。
- gVisor 在 protobuf 中对应 `RUNTIME_TYPE_CONTAINER`，HTTP/CLI 对外使用 `gvisor`。
- API 示例默认直连 `boxapi`：`http://127.0.0.1:8080`。
- exec、shell 和 sandbox 服务代理默认经过 `boxproxy`：`http://127.0.0.1:8082`。

