# 安全模型

## Firecracker

Firecracker 提供 MicroVM 边界。启用 jailer 时还包括：

- chroot 文件系统视图。
- 非特权 UID/GID。
- PID namespace。
- sandbox network namespace。
- Firecracker seccomp。
- 仅 bind mount runtime 必需文件。

Jailer chroot 中只应出现 kernel、rootfs、snapshot、metrics 和 API socket 等必要资源。

## gVisor

gVisor 使用 runsc sandbox 和 OCI namespace 隔离。它不是硬件 VM；安全假设和 Firecracker 不同。选择 runtime 时应根据 workload 风险、性能和设备需求决定。

## NVIDIA GPU

GPU 访问会扩大 sandbox 可见的宿主机设备和 driver surface：

- 仅 gVisor 路径实现 GPU 注入。
- 依赖 NVIDIA CDI spec。
- 会暴露请求的 `/dev/nvidia*` 设备。
- 会注入 CUDA/NVML driver libraries 和环境变量。
- `NVIDIA_VISIBLE_DEVICES`、`CUDA_VISIBLE_DEVICES` 和 `NVIDIA_DRIVER_CAPABILITIES` 会进入 sandbox。

CDI spec 属于高权限宿主机配置，应由管理员生成和审查，不应接受不可信用户直接上传。

## 网络和控制面

默认服务监听 loopback，Caddy 承担本地 HTTPS 和 wildcard 路由。当前本地兼容部署通常使用 dummy API key；这不等同于生产级认证。将 boxapi、boxlet、boxproxy 或 shim socket 暴露到不可信网络前，需要额外的认证、TLS、访问控制和防火墙策略。

## Artifact

从不可信 Docker image 构建 template 会执行 image 内命令和用户提供的 build steps。生产环境应限制允许的 registry、build 命令、网络访问、secret 注入和宿主机目录挂载。

