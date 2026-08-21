# 架构概览

NovitaBox 由宿主机控制面、节点 agent、runtime shim、guest agent 和代理服务组成。

详细主题：

- [组件职责](components.md)
- [运行时和能力矩阵](runtime.md)
- [网络模型](network.md)
- [Artifact 模型](artifact.md)
- [文件布局](file-layout.md)
- [生命周期](lifecycle.md)
- [安全模型](security.md)

```text
Client / SDK / CLI
        |
        | HTTP / WebSocket
        v
     boxapi
        |
        +--> boxlet
        |      - 管理本机 sandbox 生命周期
        |      - 准备 rootfs、snapshot、kernel、agent disk 或 gVisor directory rootfs
        |      - 创建 network namespace、veth、tap 或 gVisor veth 地址、路由和 NAT
        |      - 构建 template 和 image
        |
        +--> sqlite metadata

     boxproxy
        |
        | sandbox exec / shell / service proxy
        v
     sandbox guest boxd

     boxshim
        |
        | one shim per sandbox
        v
     Firecracker / gVisor / runtime process
```

## 组件

### boxapi

`boxapi` 是 HTTP API 入口，负责：

- template、sandbox、image API
- SDK/CLI 兼容路由
- 调用 boxlet 执行本机操作
- 返回统一 JSON 错误

### boxlet

`boxlet` 是节点 agent，负责：

- 管理 sandbox artifact
- 启动和连接 boxshim
- 分配网络 slot
- 创建 network namespace、tap、veth、路由和 NAT
- 构建 template 和 image
- 维护 sqlite 元数据

### boxshim

`boxshim` 是每个 sandbox 的 runtime supervisor，负责：

- 作为 Firecracker、gVisor 等 runtime 的父进程
- 通过 Unix socket 暴露 runtime RPC
- 执行 start、pause、resume、stop、reboot、kill
- 为 Firecracker 提供 balloon target、statistics 和 free-page hinting
- 在 boxlet 重启时继续托管 runtime

runtime driver：

- Firecracker：MicroVM、kernel、tap、`rootfs.ext4`、`memfile/snapfile`
- Firecracker Balloon：virtio-balloon、metrics、动态回收和 page hinting
- gVisor：`runsc`、OCI bundle、directory rootfs、可选 NVIDIA GPU
- Cloud Hypervisor：通过同一套 capability 模型暴露

gVisor GPU 通过 `runsc --nvproxy` 和 NVIDIA CDI 实现。宿主机需要 NVIDIA driver、`nvidia-ctk`、`nvidia-cdi-hook`，并生成 `/etc/cdi/nvidia.yaml`。

### boxd

`boxd` 运行在 guest 内部，负责：

- health check
- exec
- interactive shell / PTY
- SDK connect 风格的 process API
- agent reexec

### boxproxy

`boxproxy` 是数据面代理，负责：

- 把 exec/shell 请求转发到 sandbox 内的 boxd
- 通过 sandbox id 查找 host access IP
- 支持 WebSocket 和 HTTP proxy

## 默认网络

```text
host_access_cidr = 10.11.0.0/16
veth_cidr        = 10.12.0.0/16
guest_ip         = 169.254.0.21
gateway_ip       = 169.254.0.22
boxd_port        = 49983
```

每个 sandbox 的 guest IP 固定，宿主机通过唯一 host access IP 访问：

```text
Firecracker:
host -> 10.11.x.y -> netns DNAT -> 169.254.0.21:49983

gVisor:
host -> 10.11.x.y -> netns veth -> boxd 0.0.0.0:49983
```

## Artifact

```text
Firecracker Image    = rootfs.ext4
Firecracker Template = rootfs.ext4 + memfile + snapfile
Firecracker Snapshot = sandbox-bound rootfs.ext4 + memfile + snapfile

gVisor Image         = rootfs directory
gVisor Template      = rootfs directory
```

Image 更适合迁移，Template 更适合快速启动，Snapshot 绑定具体 sandbox 生命周期。gVisor 当前不支持 Firecracker 风格的 pause/resume snapshot。

## 控制面与数据面

控制面请求进入 `boxapi`，由 `boxlet` 管理节点资源，再由每 sandbox 的 `boxshim` 操作 runtime：

```text
HTTP -> boxapi -> gRPC -> boxlet -> Unix gRPC -> boxshim -> runtime
```

exec、shell 和用户服务流量进入 `boxproxy`，根据 sandbox 元数据找到唯一 host access IP，再转发给 sandbox 内的 `boxd`：

```text
HTTP/WebSocket -> boxproxy -> host access IP -> boxd
```

这两个平面分开后，runtime 生命周期失败不会直接破坏代理进程，控制面重启也不要求立即杀死由 boxshim 托管的 runtime。
