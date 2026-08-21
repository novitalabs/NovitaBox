# 安装与服务检查

## 选择部署方式

| 场景 | 推荐方式 |
| --- | --- |
| Linux 直接运行 Firecracker | Linux 安装脚本，宿主机提供 KVM 和 TUN/TAP |
| Linux 运行 gVisor | Linux 安装脚本并提供 `RUNSC_VERSION`、`RUNSC_URL` 或 `RUNSC_PATH` |
| macOS 本地开发 | macOS + Lima 安装脚本 |
| 只验证 HTTP/CLI | 可关闭 DNS/Caddy，直接访问 loopback 端口 |

## Linux 环境要求

- x86_64 或 arm64 Linux。
- Firecracker/Cloud Hypervisor 需要 `/dev/kvm`。
- Firecracker 网络需要 `/dev/net/tun`。
- Docker 用于导出 Docker/OCI image rootfs。
- `iproute2`、`iptables`、`mkfs.ext4`、`debugfs` 等系统工具。
- 安装脚本要求 Btrfs root，并执行 reflink 能力检查。
- gVisor 需要 `runsc`。
- gVisor GPU 需要 NVIDIA driver、`nvidia-ctk`、`nvidia-cdi-hook` 和 CDI spec。

如果要让 gVisor sandbox 直接使用 OverlayBD image，还需要按
[gVisor 使用 OverlayBD rootfs](./overlaybd.md) 部署额外的节点依赖：
`containerd`、`overlaybd-tcmu`、`overlaybd-snapshotter`、带 `rpull` 的
OverlayBD 扩展版 `ctr`，以及启用 TCMU、configfs 和 OverlayFS 的 Linux
内核。OverlayBD 是可选的 rootfs provider，普通 image/template 流程不依赖它。

检查：

```bash
test -e /dev/kvm && echo KVM_OK
test -e /dev/net/tun && echo TUN_OK
docker version
ip -V
iptables --version
```

## Release 安装

Linux：

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/install-release.sh | \
  RELEASE_VERSION=<release-version> sudo -E bash
```

macOS + Lima：

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/install-release.sh | \
  RELEASE_VERSION=<release-version> bash
```

`install-release.sh` 会下载平台对应的预编译组件，再调用平台安装脚本。runtime assets 可以独立使用 `RUNTIME_ASSET_VERSION`，因此应用版本和 Firecracker/kernel 资产版本可以不同。

网络受代理限制时可设置：

```bash
CURL_PROXY=http://proxy.example:8080
```

## 从源码安装

```bash
sudo -E scripts/install-linux.sh
```

常用变量：

| 变量 | 默认值/作用 |
| --- | --- |
| `ROOT_DIR` | `/data/novitabox` |
| `IMAGE_PATH` | `/data/novitabox.img` |
| `IMAGE_SIZE` | `50G` |
| `DOMAIN` | `novitabox.localhost` |
| `ENABLE_DNS` | `1` |
| `ENABLE_CADDY` | `1` |
| `REQUIRE_KVM` | `1` |
| `BOXAPI_ADDR` | `127.0.0.1:8080` |
| `BOXLET_ADDR` | `127.0.0.1:8081` |
| `BOXPROXY_ADDR` | `127.0.0.1:8082` |
| `INSTALL_GO` | `auto` |
| `SKIP_BUILD` | `0` |
| `CONFIGURE_DOCKER_MIRRORS` | `0`，默认不修改 Docker mirror |

runtime asset 可以通过本地路径、URL 或版本控制：

```text
FIRECRACKER_PATH / FIRECRACKER_URL
KERNEL_PATH      / KERNEL_URL
JAILER_PATH      / JAILER_URL
RUNSC_PATH       / RUNSC_URL / RUNSC_VERSION
```

示例：

```bash
ROOT_DIR=/data/novitabox \
IMAGE_SIZE=100G \
RUNSC_VERSION=<runsc-release> \
sudo -E scripts/install-linux.sh
```

不配置本地域名和 HTTPS：

```bash
ENABLE_DNS=0 ENABLE_CADDY=0 sudo -E scripts/install-linux.sh
```

## macOS + Lima

macOS 安装脚本会准备 Lima VM，并在 VM 内运行 Linux 组件。主要变量：

```text
VM_NAME
LIMA_CPUS
LIMA_MEMORY
LIMA_DISK
LIMA_IMAGE_PATH / LIMA_IMAGE_URL / LIMA_IMAGE_DIGEST
RUNSC_PATH / RUNSC_URL / RUNSC_VERSION
ENABLE_MAC_DNS
ENABLE_MAC_PROXY
```

Firecracker 是否能在 Lima 内运行取决于嵌套虚拟化/KVM 能力。macOS 本地环境若无法使用 Firecracker full snapshot，可以优先验证 gVisor 路径。

## NVIDIA CDI

```bash
nvidia-smi
sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
nvidia-ctk cdi list
```

确认 `runsc`：

```bash
/data/novitabox/runsc --version
/data/novitabox/boxctl runtime capabilities gvisor
```

## 服务检查

```bash
systemctl status novitabox-boxlet novitabox-boxapi novitabox-boxproxy --no-pager
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:8082/healthz
```

日志：

```bash
journalctl -u novitabox-boxlet -n 200 --no-pager
journalctl -u novitabox-boxapi -n 200 --no-pager
journalctl -u novitabox-boxproxy -n 200 --no-pager
```

文件系统和资产：

```bash
findmnt /data/novitabox
ls -l /data/novitabox/{boxapi,boxlet,boxproxy,boxshim,boxd,boxctl}
ls -l /data/novitabox/{firecracker,jailer,vmlinux.bin,runsc} 2>/dev/null
```

## 卸载

Linux：

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/uninstall-linux.sh | \
  sudo -E FORCE=1 bash
```

macOS：

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/uninstall-macos-lima.sh | \
  FORCE=1 bash
```

卸载会影响服务、mount、Lima VM 或本地数据。执行前确认 `$ROOT` 中没有需要保留的 template、image 和 sandbox。
