# NovitaBox 使用手册

这份文档按实际使用顺序组织：安装、检查服务、构建 template、创建 sandbox、进入 shell、管理生命周期、Firecracker Balloon、网络和排错。按主题拆分的文档入口见 [中文文档首页](README.md)。

## 1. 环境要求

NovitaBox 支持单机 Linux 部署，也支持 macOS 通过 Lima 启动 Linux VM。Linux 直接部署需要：

- Linux x86_64 或 arm64
- Firecracker 和 Cloud Hypervisor 需要 KVM：`/dev/kvm`
- Firecracker tap 网络需要 TUN/TAP：`/dev/net/tun`
- Docker：用于从 Docker image 导出 rootfs
- 支持 reflink 的文件系统，推荐 Btrfs
- `iptables`、`iproute2`、`debugfs`、`mkfs.ext4`
- gVisor sandbox 需要 `runsc`。release installer 可以在设置 `RUNSC_VERSION` 时自动下载。
- gVisor GPU sandbox 需要宿主机 NVIDIA driver、`nvidia-ctk`、`nvidia-cdi-hook` 和 CDI spec

macOS 安装需要：

- Homebrew
- Lima
- 支持 Apple Virtualization Framework 的 macOS 环境

一键安装脚本会在 macOS 上自动安装 Homebrew 和 Lima（如果缺失）。

如果使用 Firecracker full snapshot，宿主机的 KVM 必须支持 Firecracker 保存 vCPU 状态。部分嵌套 KVM 环境会失败，典型错误是：

```text
Failed to get KVM vcpu msr: 0x3a
```

这表示 guest 已经启动，但 Firecracker 创建 full snapshot 失败。

## 2. 一键安装（推荐）

直接从 GitHub Release 下载预编译组件安装，不需要本地编译。

Linux：

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/install-release.sh | RELEASE_VERSION=<release-version> sudo -E bash
```

macOS + Lima：

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/install-release.sh | RELEASE_VERSION=<release-version> bash
```

macOS 默认不会在宿主机配置 DNS 和 proxy。如果需要 `https://novitabox.localhost` 和 `https://*.novitabox.localhost`：

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/install-release.sh | \
  RELEASE_VERSION=<release-version> ENABLE_MAC_DNS=1 ENABLE_MAC_PROXY=1 bash
```

安装脚本会根据系统和架构下载对应组件：

```text
Linux:  novitabox-linux-amd64.tar.gz 或 novitabox-linux-arm64.tar.gz
macOS:  novitabox-darwin-<arch>.tar.gz + novitabox-linux-<arch>.tar.gz
```

`firecracker` 和 `vmlinux.bin` 默认继续从稳定的 `v0.0.1` runtime assets 下载。

卸载：

```bash
# Linux
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/uninstall-linux.sh | sudo -E FORCE=1 bash

# macOS + Lima
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/uninstall-macos-lima.sh | FORCE=1 bash
```

## 3. 从源码安装

在项目根目录执行：

```bash
sudo -E scripts/install-linux.sh
```

常用参数：

```bash
ROOT_DIR=/data/novitabox \
IMAGE_PATH=/data/novitabox.img \
IMAGE_SIZE=100G \
DOMAIN=novitabox.localhost \
sudo -E scripts/install-linux.sh
```

如果已经准备好 Firecracker 和 kernel：

```bash
FIRECRACKER_PATH=/path/to/firecracker \
KERNEL_PATH=/path/to/vmlinux.bin \
sudo -E scripts/install-linux.sh
```

如果只需要本机 `127.0.0.1` 调试，不配置 Caddy：

```bash
ENABLE_CADDY=0 sudo -E scripts/install-linux.sh
```

安装脚本会完成：

- 创建并挂载 Btrfs root
- 编译 `boxapi`、`boxctl`、`boxd`、`boxlet`、`boxproxy`、`boxshim`
- 安装 Firecracker 和 guest kernel
- 使用 `$ROOT_DIR/runsc` 启动 gVisor sandbox（如果存在）
- 写入 systemd 服务
- 配置 IP forwarding
- 可选配置 dnsmasq 和 Caddy

如果需要 gVisor GPU，先在宿主机生成 NVIDIA CDI spec：

```bash
sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
```

## 4. 检查服务

```bash
systemctl status novitabox-boxlet novitabox-boxapi novitabox-boxproxy --no-pager

curl http://127.0.0.1:8080/health
curl http://127.0.0.1:8082/healthz
```

如果开启 Caddy：

```bash
curl -k https://novitabox.localhost/health
```

日志：

```bash
journalctl -u novitabox-boxlet -f
journalctl -u novitabox-boxapi -f
journalctl -u novitabox-boxproxy -f
```

## 5. 构建 Template

从 Docker image 构建：

```bash
/data/novitabox/boxctl \
  --api http://127.0.0.1:8080 \
  template build my-template \
  --from-image ubuntu:22.04 \
  --run 'echo hello from novitabox'
```

安装软件：

```bash
/data/novitabox/boxctl template build python-template \
  --from-image ubuntu:22.04 \
  --run 'apt-get update' \
  --run 'apt-get install -y python3 python3-pip' \
  --exec 'python3 --version'
```

命令说明：

- `--run`：通过 `/bin/sh -c` 执行，适合 shell 命令。
- `--exec`：按空格切分后直接执行，适合简单二进制命令。
- `--template`：指定 template id；不指定则自动生成 `tpl-...`。
- `--cpu`、`--memory`：设置构建时 VM 资源。
- 当前 `boxctl template build` 没有 `--runtime` 参数，CLI 默认创建 Firecracker template。

构建 gVisor template 时，需要先通过 v3 API 设置 `runtimeType: "gvisor"`：

```bash
curl -sS -X POST http://127.0.0.1:8080/v3/templates \
  -H 'Content-Type: application/json' \
  -d '{"name":"cuda-template","runtimeType":"gvisor"}'
```

从返回值取得 `templateID` 和 `buildID` 后：

```bash
curl -sS -X POST \
  http://127.0.0.1:8080/v2/templates/<templateID>/builds/<buildID> \
  -H 'Content-Type: application/json' \
  -d '{"fromImage":"nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda11.7.1-ubi8"}'
```

查看 template：

```bash
/data/novitabox/boxctl template list
/data/novitabox/boxctl template get tpl-xxxxxxxxxxxxxxxxxxxx
```

删除 template：

```bash
/data/novitabox/boxctl template delete tpl-xxxxxxxxxxxxxxxxxxxx
```

## 6. 创建 Sandbox

```bash
/data/novitabox/boxctl \
  --api http://127.0.0.1:8080 \
  sandbox create tpl-xxxxxxxxxxxxxxxxxxxx
```

返回结果里会包含 `sandboxID`，格式为：

```text
sbx-xxxxxxxxxxxxxxxxxxxx
```

查看：

```bash
/data/novitabox/boxctl sandbox list
/data/novitabox/boxctl sandbox get sbx-xxxxxxxxxxxxxxxxxxxx
```

创建 gVisor sandbox：

```bash
/data/novitabox/boxctl sandbox create \
  --template tpl-xxxxxxxxxxxxxxxxxxxx \
  --runtime gvisor
```

创建带 1 张 NVIDIA GPU 的 gVisor sandbox：

```bash
/data/novitabox/boxctl sandbox create \
  --template tpl-xxxxxxxxxxxxxxxxxxxx \
  --runtime gvisor \
  --gpu 1
```

验证 GPU：

```bash
/data/novitabox/boxctl sandbox exec sbx-xxxxxxxxxxxxxxxxxxxx /usr/bin/nvidia-smi
/data/novitabox/boxctl sandbox exec sbx-xxxxxxxxxxxxxxxxxxxx /cuda-samples/vectorAdd
```

`nvidia-smi` 能显示 GPU 表示 NVML/driver 通路正常。`vectorAdd` 输出 `Test PASSED` 表示 CUDA kernel 执行正常。

## 7. 执行命令和进入 Shell

执行命令：

```bash
/data/novitabox/boxctl \
  --proxy http://127.0.0.1:8082 \
  exec sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh -c 'pwd && ls -alh'
```

交互式 shell：

```bash
/data/novitabox/boxctl \
  --proxy http://127.0.0.1:8082 \
  exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
```

也可以使用 sandbox 子命令：

```bash
/data/novitabox/boxctl sandbox exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
/data/novitabox/boxctl sandbox shell sbx-xxxxxxxxxxxxxxxxxxxx
```

如果 rootfs 中没有 `/bin/bash`，使用 `/bin/sh`。

## 8. Sandbox 生命周期

```bash
/data/novitabox/boxctl sandbox pause sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox resume sbx-xxxxxxxxxxxxxxxxxxxx

/data/novitabox/boxctl sandbox poweroff sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox poweron sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox reboot sbx-xxxxxxxxxxxxxxxxxxxx

/data/novitabox/boxctl sandbox delete sbx-xxxxxxxxxxxxxxxxxxxx
```

删除命令也支持：

```bash
/data/novitabox/boxctl sandbox rm sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox kill sbx-xxxxxxxxxxxxxxxxxxxx
```

## 9. Firecracker Balloon

Firecracker sandbox 默认创建 virtio-balloon，初始回收目标为 `0 MiB`。动态设置 target：

```bash
/data/novitabox/boxctl sandbox balloon set sbx-xxxxxxxxxxxxxxxxxxxx --amount-mib 1024
/data/novitabox/boxctl sandbox balloon get sbx-xxxxxxxxxxxxxxxxxxxx
```

把 target 设回零会 deflate balloon：

```bash
/data/novitabox/boxctl sandbox balloon set sbx-xxxxxxxxxxxxxxxxxxxx --amount-mib 0
```

查看 guest 统计并修改轮询间隔：

```bash
/data/novitabox/boxctl sandbox balloon stats sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox balloon stats-interval sbx-xxxxxxxxxxxxxxxxxxxx --interval-s 2
```

执行一次 free-page hinting：

```bash
/data/novitabox/boxctl sandbox balloon hinting start sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox balloon hinting get sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox balloon hinting stop sbx-xxxxxxxxxxxxxxxxxxxx
```

注意：

- Balloon 只由 Firecracker runtime 支持。
- guest kernel 必须启用 virtio-balloon。
- `amountMiB` 是回收目标，不是修改 VM 总内存。
- free-page hinting 是一次性 run，不是后台周期任务。
- Firecracker metrics 写入 sandbox 目录的 `firecracker-metrics.json`。

## 10. Image

Image 是 rootfs-only artifact，不包含内存和 Firecracker snapshot。

Firecracker image 使用 `rootfs.ext4`。gVisor image 使用 directory rootfs。

从 template 创建 image：

```bash
/data/novitabox/boxctl image create tpl-xxxxxxxxxxxxxxxxxxxx \
  --image img-xxxxxxxxxxxxxxxxxxxx
```

查看和删除：

```bash
/data/novitabox/boxctl image list
/data/novitabox/boxctl image get img-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl image delete img-xxxxxxxxxxxxxxxxxxxx
```

也可以通过 template convert：

```bash
/data/novitabox/boxctl template convert tpl-xxxxxxxxxxxxxxxxxxxx \
  --image img-xxxxxxxxxxxxxxxxxxxx
```

## 11. SDK 和 CLI 环境变量

如果使用 Caddy 和 `novitabox.localhost`：

```bash
export NOVITA_DOMAIN=novitabox.localhost
export NOVITA_API_KEY=dummy
export NOVITA_ACCESS_TOKEN=dummy
export NO_PROXY=.novitabox.localhost,localhost,127.0.0.1,::1
export NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/caddy-local.crt
```

Node CLI：

```bash
npm install -g novita-sandbox-cli@2.0.5
novita-sandbox-cli sbx create tpl-xxxxxxxxxxxxxxxxxxxx
```

Python SDK：

```python
import os
from novita_sandbox import Sandbox

os.environ["NOVITA_DOMAIN"] = "novitabox.localhost"
os.environ["NOVITA_API_KEY"] = "dummy"
os.environ["NOVITA_ACCESS_TOKEN"] = "dummy"

sbx = Sandbox(template="tpl-xxxxxxxxxxxxxxxxxxxx")
result = sbx.commands.run("echo hello")
print(result.stdout)
sbx.close()
```

## 12. 网络模型

默认网络：

```text
host_access_cidr = 10.11.0.0/16
veth_cidr        = 10.12.0.0/16
guest_ip         = 169.254.0.21
gateway_ip       = 169.254.0.22
boxd_port        = 49983
```

每个 sandbox 有唯一的 host access IP，但 guest 内部 IP 固定：

```text
Firecracker:
host -> 10.11.x.y -> netns DNAT -> 169.254.0.21:49983

gVisor:
host -> 10.11.x.y -> netns veth -> boxd 0.0.0.0:49983
```

gVisor 没有 VM tap 设备。`boxd` 作为 `runsc` sandbox 的 init 进程运行在 network namespace 里，`boxlet` 会把唯一的 host access IP 配到 namespace veth 上，并把兼容用的 `169.254.0.21/30` 配到 loopback。

检查网络：

```bash
ip netns list
ip route | grep 10.11
journalctl -u novitabox-boxlet -n 200 --no-pager
```

## 13. 常见问题

### template build 报 no route to host

先看 Firecracker 日志。很多时候 `no route to host` 是表象，真实原因是 VM 已退出。

```bash
find /data/novitabox/sandboxes -name firecracker.log -print
tail -200 /data/novitabox/sandboxes/<build-id>/firecracker.log
```

如果看到：

```text
Requested init /novitabox/init failed
```

说明 rootfs 里没有正确注入 init。

如果看到：

```text
starting boxd
Failed to get KVM vcpu msr: 0x3a
```

说明 guest 已启动，boxd 已启动，但 Firecracker snapshot 创建失败。

### boxctl exec 报 executable file not found

检查命令是否在 rootfs 中存在：

```bash
/data/novitabox/boxctl exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
```

Ubuntu 基础镜像一般有 `/bin/sh`，不一定有 `/bin/bash`。

### SDK HTTPS 证书错误

确认 Caddy local CA 已安装：

```bash
ls -l /usr/local/share/ca-certificates/caddy-local.crt
sudo update-ca-certificates
```

Node CLI 需要：

```bash
export NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/caddy-local.crt
```

### 服务启动了但没有进程

检查 systemd：

```bash
systemctl status novitabox-boxlet novitabox-boxapi novitabox-boxproxy --no-pager
journalctl -u novitabox-boxlet -n 200 --no-pager
```

确认 `/data/novitabox` 是 Btrfs 挂载点：

```bash
findmnt /data/novitabox
```
