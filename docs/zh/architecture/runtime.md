# 运行时与能力矩阵

## RuntimeSpec

`RuntimeSpec` 是 boxlet 与 boxshim 的内部契约，包含：

- sandbox ID 和 runtime type
- vCPU、内存、hugepages、GPU
- kernel、rootfs 和 snapshot 路径
- network、agent、jailer 和 extra drives
- Firecracker balloon 初始配置
- labels 和 annotations

runtime-specific 参数应在 driver 内解释，上层不应直接拼接 Firecracker API 或 OCI spec。

## Firecracker

Firecracker 是默认 runtime：

- MicroVM 隔离。
- `rootfs.ext4`。
- guest kernel。
- `memfile`、`snapfile` full snapshot。
- network namespace 内的 tap 网络。
- vsock、serial console 和 jailer。
- virtio-balloon、statistics、free-page hinting/reporting。

Firecracker 当前不支持 diff snapshot，GPU capability 为 false。

## gVisor

gVisor driver 使用 `runsc`：

- directory rootfs。
- OCI bundle：`bundle/config.json`。
- 使用 boxlet 已准备好的 network namespace。
- 没有 Firecracker tap、kernel、memfile 和 snapfile。
- 不支持 Firecracker 风格 pause/resume snapshot。
- 可通过 `runsc --nvproxy` 和 NVIDIA CDI 使用 GPU。

GPU 流程会读取 `/etc/cdi/nvidia.yaml` 等 CDI spec，注入请求的 `/dev/nvidia*`、driver libraries、环境变量和受支持 hooks。`update-ldcache` hook 不直接在 runsc 中执行；NovitaBox 注入 library path，并通过 CDI symlink hook 准备 `libcuda.so.1` 和 `libnvidia-ml.so.1`。

gVisor 也可以使用 OverlayBD 作为 rootfs provider。containerd 和现成的 OverlayBD snapshotter 管理 image、active snapshot、远程读取和本地 writable upper；NovitaBox 仍然直接管理 `runsc`。创建 sandbox 时执行 `rpull`、创建独立 active snapshot、挂载到 `sandboxes/<id>/rootfs`、注入 boxd，然后启动 runsc。poweroff 只卸载并保留 snapshot，poweron 使用持久化 snapshot key 重新挂载，kill 通过 snapshotter API 删除。该路径不需要 NovitaBox template build。

## Cloud Hypervisor

Cloud Hypervisor 通过相同 capability 模型暴露。节点 capability 当前声明 image/template/snapshot、pause/resume、GPU、vsock、tap、hotplug 和 jailer 等能力；实际使用前仍应确认对应 runtime driver 和二进制已部署。

## 能力矩阵

| 能力 | Firecracker | gVisor | Cloud Hypervisor |
| --- | --- | --- | --- |
| Image 启动 | 是 | 是 | 是 |
| Template 启动 | 是 | 是 | 是 |
| Snapshot 启动 | 是 | 否 | 是 |
| Pause/Resume | 是 | 否 | 是 |
| Full snapshot | 是 | 否 | 是 |
| Diff snapshot | 否 | 否 | 否 |
| GPU | 否 | 是 | 节点能力声明为是 |
| vsock | 是 | 否 | 是 |
| tap network | 是 | 否 | 是 |
| Balloon | boxshim 支持 | 否 | 未实现 |
| Graceful shutdown | 是 | 是 | 是 |
| Serial console | 是 | 否 | 是 |
| Jailer | 是 | 否 | 是 |

### 当前已知 capability 差异

Firecracker boxshim driver 已设置 `balloon=true`，balloon HTTP/RPC 也会直接向 sandbox boxshim 查询 capability 后执行。但 boxlet 的静态 `firecrackerCapabilities()` 当前尚未设置 balloon 字段，因此 `/v1/runtimes/firecracker/capabilities` 可能显示 `balloon=false`。判断实际 balloon 操作是否可用时，以 Firecracker sandbox 的 balloon API 返回为准，直到两层 capability 同步。
