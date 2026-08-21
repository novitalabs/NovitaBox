\
# gVisor 使用 OverlayBD rootfs

本文说明如何在 Linux 节点上部署 OverlayBD 运行依赖，并让 NovitaBox 的 gVisor sandbox 直接使用已经转换好的 OverlayBD image。这里的 OverlayBD image 指已经通过 OverlayBD converter 生成并发布到 registry 的镜像，不是普通 OCI 镜像。

当前实现复用 containerd 和现成的 OverlayBD snapshotter。NovitaBox 不实现新的 snapshotter daemon，也不通过 template build 生成 OverlayBD rootfs。调用链如下：

```text
boxlet
  ├─ 执行 OverlayBD 扩展版 ctr rpull
  ├─ 连接 /run/containerd/containerd.sock
  └─ 调用 containerd SnapshotService("overlaybd")
                         │
                         ▼
              overlaybd-snapshotter
              /run/overlaybd-snapshotter/overlaybd.sock
                         │
                         ▼
                  overlaybd-tcmu
                         │
                         ▼
                 TCMU 虚拟块设备
                         │
                         ▼
                 ext4 + Linux OverlayFS
                         │
                         ▼
                 sandboxes/<id>/rootfs
                         │
                         ▼
                         runsc
```

## 一、节点需要部署哪些组件

| 组件 | 作用 | 运行时是否需要 |
| --- | --- | --- |
| `containerd` | 保存 image/content 元数据、lease，并提供 snapshot API 和 proxy plugin 路由 | 是 |
| `overlaybd-snapshotter` | 实现 containerd 的 OverlayBD snapshot API，创建 committed/active snapshot 并返回 mount 参数 | 是 |
| `overlaybd-tcmu` | OverlayBD 用户态 backstore，提供虚拟块设备和 lazy read | 是 |
| OverlayBD 扩展版 `ctr` | 提供 `rpull`，在节点上准备 OverlayBD image | 未缓存镜像时需要 |
| OverlayBD converter/commit 工具 | 把普通 OCI/Docker image 转换成 OverlayBD image | 只在构建/发布镜像的机器需要 |
| Linux `target_core_user` | TCMU 的内核接口 | 是 |
| Linux `overlay` | 将 lower rootfs 和 active writable layer 组合成最终目录 | 是 |
| NovitaBox `boxlet`、`boxshim`、`runsc`、`boxd` | 管理 sandbox 并启动 gVisor | 是 |

每次文件读取不需要经过 containerd。containerd 和 snapshotter 只负责 image 元数据、snapshot 生命周期和 mount 准备；挂载完成后，文件访问由 OverlayFS、OverlayBD/TCMU 和本地 cache 完成。

## 二、Linux 内核模块和内核准备

### 需要的能力

OverlayBD 的关键内核能力是 TCMU，通常涉及以下模块：

- `target_core_user`：OverlayBD TCMU 用户态 backstore 使用的核心模块；
- `target_core_mod`：target core 基础模块，通常会作为依赖自动加载；
- `uio`：部分发行版会作为 TCMU 依赖自动加载，不一定需要手工加载；
- `overlay`：最终 sandbox rootfs 使用 Linux OverlayFS；
- `configfs`：target core/TCMU 使用 configfs 创建 target 对象；
- `ext4`：OverlayBD 转换出来的文件系统通常以 ext4 形式提供，若内核内置则不需要单独加载。

模块名会因发行版和内核配置略有不同。首先确认当前内核：

```bash
uname -a
lsmod | grep -E 'target_core_user|target_core_mod|uio|overlay' || true
grep -E '(^| )overlay( |$)' /proc/filesystems
grep -E '(^| )ext4( |$)' /proc/filesystems || true
mountpoint /sys/kernel/config || true
test -d /sys/kernel/config/target && echo 'TCMU configfs target: ok' || true
```

如果出现 `Module target_core_user not found`，需要安装与 `uname -r` 完全匹配的 kernel modules 包，或者换用打开 TCMU/OverlayFS 配置的内核。不要只安装用户态 OverlayBD 二进制后忽略内核模块。

### 立即加载模块

```bash
sudo modprobe configfs
sudo modprobe target_core_mod
sudo modprobe target_core_user
sudo modprobe overlay
sudo modprobe ext4
```

`target_core_mod`、`uio` 等依赖可能会被 `modprobe target_core_user` 自动加载。如果模块已经编译进内核或已经加载，命令返回成功即可。

确保 configfs 已挂载：

```bash
sudo mkdir -p /sys/kernel/config
mountpoint -q /sys/kernel/config || \
  sudo mount -t configfs configfs /sys/kernel/config
```

### 重启后自动加载

```bash
sudo install -d /etc/modules-load.d
sudo tee /etc/modules-load.d/novitabox-overlaybd.conf >/dev/null <<'EOF_MODULES'
configfs
target_core_mod
target_core_user
overlay
ext4
EOF_MODULES
```

有些 systemd 发行版会自动挂载 configfs；这时保留 modules-load 配置即可，不要在 `/etc/fstab` 再重复添加相同挂载项。

常见错误：

- `cannot create ... /sys/kernel/config/target`：configfs 没挂载，或 `target_core_user` 没加载；
- `unknown filesystem type overlay`：内核没有 OverlayFS；
- TCMU 服务启动但没有设备：查看 `journalctl -u overlaybd-tcmu`，并确认 target core 模块；
- `modprobe` 找不到模块：安装匹配当前内核版本的 modules 包，而不是随意安装另一版本的包。

## 三、编译和安装 OverlayBD backstore

OverlayBD backstore 来自 `containerd/overlaybd`。它提供 `overlaybd-tcmu`、`overlaybd-create`、`overlaybd-commit` 等原生组件。snapshotter 和扩展版 `ctr` 来自另一个仓库，见下一节。

### Debian/Ubuntu 构建依赖

```bash
sudo apt-get update
sudo apt-get install -y \
  build-essential cmake git pkg-config automake libtool \
  libaio-dev libcurl4-openssl-dev libssl-dev \
  libnl-3-dev libnl-genl-3-dev libgflags-dev \
  libzstd-dev libext2fs-dev
```

RPM 系发行版使用上游文档对应的包名。生产环境应固定 OverlayBD release 或 commit，并确保生成镜像的 converter、节点上的 backstore 使用兼容版本，不要直接跟随未验证的 upstream HEAD。

### 编译并安装

```bash
git clone https://github.com/containerd/overlaybd.git
cd overlaybd
git submodule update --init

cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j"$(nproc)"
sudo cmake --install build
```

通常安装到 `/opt/overlaybd/`。验证：

```bash
test -x /opt/overlaybd/bin/overlaybd-tcmu
test -x /opt/overlaybd/bin/overlaybd-create
test -x /opt/overlaybd/bin/overlaybd-commit
ls -l /etc/overlaybd/overlaybd.json
```

如果使用了不同的 `CMAKE_INSTALL_PREFIX`，要么把二进制安装到 systemd unit 使用的路径，要么同步修改 unit 和配置文件，不能只替换其中一处。

### 配置 cache 和 registry 凭据

配置文件一般位于 `/etc/overlaybd/`。下面只是配置结构示例，必须以实际构建版本随附的 schema 为准：

```json
{
  "logConfig": {
    "logLevel": 1,
    "logPath": "/var/log/overlaybd.log"
  },
  "cacheConfig": {
    "cacheType": "file",
    "cacheDir": "/opt/overlaybd/registry_cache",
    "cacheSizeGB": 100
  },
  "gzipCacheConfig": {
    "enable": true,
    "cacheDir": "/opt/overlaybd/gzip_cache",
    "cacheSizeGB": 20
  },
  "credentialConfig": {
    "mode": "file",
    "path": "/opt/overlaybd/cred.json"
  }
}
```

远程镜像节点至少要规划 registry cache、gzip cache 的容量和权限。私有 registry 要按当前 OverlayBD 版本配置 credentials，例如：

```json
{
  "auths": {
    "registry.example.com": {
      "username": "REGISTRY_USER",
      "password": "REGISTRY_PASSWORD"
    }
  }
}
```

```bash
sudo install -o root -g root -m 0600 /path/to/cred.json /opt/overlaybd/cred.json
```

不要把 registry 密码提交到仓库或放进公开部署包。`/root/.docker/config.json` 不一定会被当前 OverlayBD backstore 使用；如果新节点出现 `pull access denied`，优先检查 OverlayBD 自己的 credential 配置。

### 部署 `overlaybd-tcmu`

可以创建 `/etc/systemd/system/overlaybd-tcmu.service`：

```ini
[Unit]
Description=OverlayBD TCMU backstore
After=network.target local-fs.target
Before=overlaybd-snapshotter.service shutdown.target

[Service]
Type=simple
ExecStartPre=/sbin/modprobe target_core_user
ExecStart=/opt/overlaybd/bin/overlaybd-tcmu
Restart=always
RestartSec=1s
KillMode=process
LimitNOFILE=1048576
LimitCORE=infinity
OOMScoreAdjust=-999

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now overlaybd-tcmu.service
sudo systemctl status overlaybd-tcmu.service --no-pager
```

如果你的构建版本需要显式配置文件参数，以 `overlaybd-tcmu --help` 和该版本的示例 unit 为准补充 `ExecStart` 参数。

## 四、编译和部署 snapshotter、扩展版 ctr

### 编译来源

OverlayBD snapshotter 和带 `rpull` 的扩展版 `ctr` 来自 `containerd/accelerated-container-image`，不是 NovitaBox 本仓库生成的组件。

```bash
git clone https://github.com/containerd/accelerated-container-image.git
cd accelerated-container-image
make
```

工具链必须跟随固定的 upstream 分支要求。当前分支的构建说明要求 Go 1.26.x、`runc >= 1.0` 和 `containerd >= 2.0.x`；不同 release 可能变化，实际以 pinned 版本文档和 `go.mod` 为准。

安装运行时组件：

```bash
sudo install -d /opt/overlaybd/snapshotter
sudo install -m 0755 bin/overlaybd-snapshotter /opt/overlaybd/snapshotter/overlaybd-snapshotter
sudo install -m 0755 bin/ctr /opt/overlaybd/snapshotter/ctr
```

确认不是普通发行版 `ctr`：

```bash
/opt/overlaybd/snapshotter/ctr rpull --help
```

帮助信息中必须存在 `rpull`。普通 `ctr images pull` 不能替代 OverlayBD 的 `rpull`。`convertor`、`overlaybd-attacher` 等工具通常只需部署在镜像构建/调试节点。

### snapshotter 配置

```bash
sudo install -d /etc/overlaybd-snapshotter /var/lib/overlaybd /run/overlaybd-snapshotter
sudo tee /etc/overlaybd-snapshotter/config.json >/dev/null <<'EOF_SNAPSHOTTER'
{
  "root": "/var/lib/overlaybd/",
  "address": "/run/overlaybd-snapshotter/overlaybd.sock"
}
EOF_SNAPSHOTTER
```

在 `/etc/containerd/config.toml` 注册 proxy snapshotter：

```toml
[proxy_plugins.overlaybd]
  type = "snapshot"
  address = "/run/overlaybd-snapshotter/overlaybd.sock"
```

同一个 plugin 只能注册一次。不要把该配置同时放在多个 include 文件里。snapshotter 的配置字段和启动参数可能随版本变化；以二进制 `--help`、仓库示例和实际安装版本为准。

### 部署 `overlaybd-snapshotter`

```ini
[Unit]
Description=OverlayBD containerd snapshotter
After=network.target local-fs.target overlaybd-tcmu.service
Before=containerd.service shutdown.target
Requires=overlaybd-tcmu.service

[Service]
Type=simple
ExecStartPre=/sbin/modprobe target_core_user
ExecStartPre=/sbin/modprobe overlay
ExecStart=/opt/overlaybd/snapshotter/overlaybd-snapshotter
Restart=always
RestartSec=1s
KillMode=process
LimitNOFILE=1048576
LimitCORE=infinity
OOMScoreAdjust=-999

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable overlaybd-tcmu.service overlaybd-snapshotter.service
```

## 五、启动顺序和检查

修改 containerd 配置后，应在维护窗口重启；containerd 或 snapshotter 停止期间，新 snapshot 操作会失败。

首次安装：

```bash
sudo systemctl enable --now overlaybd-tcmu.service
sudo systemctl enable --now overlaybd-snapshotter.service
sudo systemctl enable --now containerd.service
```

检查：

```bash
systemctl is-active containerd overlaybd-tcmu overlaybd-snapshotter
test -S /run/containerd/containerd.sock
test -S /run/overlaybd-snapshotter/overlaybd.sock
test -x /opt/overlaybd/bin/overlaybd-tcmu
test -x /opt/overlaybd/snapshotter/overlaybd-snapshotter
test -x /opt/overlaybd/snapshotter/ctr
ss -xlpn | grep -E 'containerd.sock|overlaybd.sock'
ctr plugins ls | grep -E 'overlaybd|snapshotter'
```

containerd 中的 `overlaybd` plugin 应为 `ok`。继续测试前先看日志：

```bash
journalctl -u overlaybd-tcmu -b --no-pager -n 100
journalctl -u overlaybd-snapshotter -b --no-pager -n 100
journalctl -u containerd -b --no-pager -n 100
```

依赖关系是：`overlaybd-tcmu` 提供设备能力，snapshotter 依赖它并监听 Unix socket，containerd 通过 proxy plugin 连接 snapshotter，boxlet 再通过 containerd client 调用 snapshot API。

## 六、启动 NovitaBox

NovitaBox 默认使用：

```text
containerd:   /run/containerd/containerd.sock
namespace:    novitabox
snapshotter:  overlaybd
ctr:          /opt/overlaybd/snapshotter/ctr
socket check: /run/overlaybd-snapshotter/overlaybd.sock
```

不需要显式增加 `--overlaybd` feature flag。请求中的 `rootfs.provider=overlaybd` 会选择该路径：

```bash
/root/novitabox/boxlet --root /root/novitabox
```

如果路径不同，再显式覆盖：

```bash
/root/novitabox/boxlet \
  --root /root/novitabox \
  --overlaybd-containerd /run/containerd/containerd.sock \
  --overlaybd-namespace novitabox \
  --overlaybd-snapshotter overlaybd \
  --overlaybd-ctr /opt/overlaybd/snapshotter/ctr \
  --overlaybd-socket /run/overlaybd-snapshotter/overlaybd.sock
```

boxapi 连接 boxlet；使用 `boxctl sandbox exec` 或 `shell` 时还需要 boxproxy。

## 七、准备 OverlayBD image

NovitaBox 创建 sandbox 时不会把普通 OCI image 在线转换成 OverlayBD image。需要先使用 converter 生成并发布：

```bash
git clone https://github.com/containerd/accelerated-container-image.git
cd accelerated-container-image
docker build -f Dockerfile -t overlaybd-convertor .
docker run --rm overlaybd-convertor \
  -r registry.hub.docker.com/library/ubuntu \
  -i 24.04 \
  -o 24.04_obd
```

具体 registry 登录、输出命名、文件系统选择和 push 参数以固定版本仓库文档为准。运行节点必须能访问生成的 OverlayBD image 和相关 blob。

当前测试节点可用的镜像是：

```text
docker.io/1135479869/overlaybd:ubuntu24.04_obd
```

注意 tag 是 `ubuntu24.04_obd`，不是 `ubuntu2404_obd`。后者会导致 registry 解析失败或 `pull access denied`。

## 八、先 rpull，再创建 sandbox

使用和 boxlet 相同的 namespace、snapshotter 和 socket：

```bash
/opt/overlaybd/snapshotter/ctr \
  --address /run/containerd/containerd.sock \
  --namespace novitabox \
  rpull \
  --snapshotter overlaybd \
  docker.io/1135479869/overlaybd:ubuntu24.04_obd
```

检查 image 和 snapshot：

```bash
/opt/overlaybd/snapshotter/ctr --namespace novitabox images ls
/opt/overlaybd/snapshotter/ctr --namespace novitabox snapshots --snapshotter overlaybd ls
```

创建 sandbox：

```bash
/root/novitabox/boxctl sandbox create \
  --overlaybd-image docker.io/1135479869/overlaybd:ubuntu24.04_obd
```

内部顺序为：

```text
rpull/解析 image
  → committed OverlayBD snapshot
  → 为 sandbox 创建独立 active writable snapshot
  → 挂载到 sandboxes/<id>/rootfs
  → 注入 boxd
  → runsc create/start
```

查询实际使用的 image、digest 和 snapshot：

```bash
/root/novitabox/boxctl sandbox list
/root/novitabox/boxctl sandbox get <sandbox-id>
findmnt /root/novitabox/sandboxes/<sandbox-id>/rootfs
```

API 中的 rootfs 类似：

```json
{
  "rootfs": {
    "provider": "overlaybd",
    "image": "docker.io/1135479869/overlaybd:ubuntu24.04_obd",
    "digest": "sha256:...",
    "snapshotKey": "novitabox-sandbox-<sandbox-id>"
  }
}
```

`findmnt` 显示最终文件系统类型为 `overlay` 是正常的：OverlayBD 提供 lower 层的块设备/文件系统，snapshotter 再返回 lowerdir、upperdir、workdir，boxlet 最终执行 Linux OverlayFS mount。

## 九、poweroff、poweron 和删除

poweroff 会卸载 rootfs，但保留 active snapshot 和 writable 数据：

```bash
boxctl sandbox poweroff <sandbox-id>
findmnt /root/novitabox/sandboxes/<sandbox-id>/rootfs || true
/opt/overlaybd/snapshotter/ctr --namespace novitabox snapshots --snapshotter overlaybd ls
```

poweron 会使用持久化的 snapshot key 重新挂载同一个 active snapshot：

```bash
boxctl sandbox poweron <sandbox-id>
boxctl sandbox exec <sandbox-id> /bin/sh -c 'cat /root/overlaybd-test/persist.txt'
```

删除 sandbox 时，NovitaBox 通过 snapshotter API 删除 active snapshot 和 lease；共享的 committed image snapshot 不会因为删除一个 sandbox 而删除：

```bash
boxctl sandbox delete <sandbox-id>
/opt/overlaybd/snapshotter/ctr --namespace novitabox snapshots --snapshotter overlaybd ls
/opt/overlaybd/snapshotter/ctr --namespace novitabox leases ls
```

## 十、故障排查

### `pull access denied` / `insufficient_scope`

先确认镜像引用和 tag：

```bash
/opt/overlaybd/snapshotter/ctr --namespace novitabox images ls
```

再检查 OverlayBD 自己的 registry 凭据。缓存节点能启动，不代表新节点可以；新节点需要重新解析 manifest 并下载 OverlayBD blobs。

### `failed to connect overlaybd snapshotter`

```bash
systemctl status overlaybd-snapshotter overlaybd-tcmu containerd --no-pager
ls -l /run/overlaybd-snapshotter/overlaybd.sock
journalctl -u overlaybd-snapshotter -u overlaybd-tcmu -u containerd -b --no-pager -n 200
```

### `/opt/overlaybd/snapshotter/ctr` 没有 `rpull`

当前安装的是普通 containerd `ctr`。重新安装 `accelerated-container-image` 生成的扩展版 `ctr`，或者通过 `--overlaybd-ctr` 指向正确的绝对路径。

### TCMU/configfs 错误

```bash
sudo modprobe configfs target_core_mod target_core_user overlay
mountpoint /sys/kernel/config
ls -la /sys/kernel/config/target
```

同时检查：

```bash
journalctl -u overlaybd-tcmu -b --no-pager -n 200
lsmod | grep -E 'target_core_user|target_core_mod|uio'
```

### rootfs 显示 `overlay` 而不是 `overlaybd`

这是预期结果。检查最终 mount 参数：

```bash
findmnt -o TARGET,FSTYPE,SOURCE,OPTIONS \
  /root/novitabox/sandboxes/<sandbox-id>/rootfs
```

### gVisor 网络报 `create tap` / `Device or resource busy`

这通常发生在 gVisor 网络 namespace 或 TUN/TAP 准备阶段，不等同于 OverlayBD 挂载失败。单独检查 boxlet 的网络配置、残留 network namespace、tap 设备和旧 sandbox；先确认 snapshotter socket 和 `findmnt` 正常，再定位网络问题。

## 十一、节点就绪检查清单

```bash
# Kernel
lsmod | grep target_core_user
grep overlay /proc/filesystems
mountpoint /sys/kernel/config

# OverlayBD
systemctl is-active overlaybd-tcmu overlaybd-snapshotter
test -S /run/overlaybd-snapshotter/overlaybd.sock
test -x /opt/overlaybd/snapshotter/ctr

# containerd
systemctl is-active containerd
test -S /run/containerd/containerd.sock
ctr plugins ls | grep overlaybd

# NovitaBox
test -x /root/novitabox/boxlet
test -x /root/novitabox/boxshim
test -x /root/novitabox/runsc
test -x /root/novitabox/boxd
```
