# 文件布局

安装脚本默认使用 `/data/novitabox`；程序配置默认根目录是 `/var/lib/novitabox`。下面统一写作 `$ROOT`。

```text
$ROOT/db/novitabox.db

$ROOT/images/<image_id>/rootfs.ext4
$ROOT/images/<image_id>/rootfs/             # gVisor directory rootfs

$ROOT/templates/<template_id>/rootfs.ext4
$ROOT/templates/<template_id>/memfile
$ROOT/templates/<template_id>/snapfile
$ROOT/templates/<template_id>/rootfs/       # gVisor directory rootfs

$ROOT/sandboxes/<sandbox_id>/shim.sock
$ROOT/sandboxes/<sandbox_id>/shim.pid
$ROOT/sandboxes/<sandbox_id>/fc.sock
$ROOT/sandboxes/<sandbox_id>/firecracker.log
$ROOT/sandboxes/<sandbox_id>/firecracker-metrics.json
$ROOT/sandboxes/<sandbox_id>/kernel
$ROOT/sandboxes/<sandbox_id>/snapshot/

$ROOT/sandboxes/<sandbox_id>/bundle/config.json
$ROOT/sandboxes/<sandbox_id>/runsc/
$ROOT/sandboxes/<sandbox_id>/runsc.log
$ROOT/sandboxes/<sandbox_id>/rootfs/

$ROOT/boxapi
$ROOT/boxctl
$ROOT/boxd
$ROOT/boxlet
$ROOT/boxproxy
$ROOT/boxshim
$ROOT/firecracker
$ROOT/jailer
$ROOT/runsc
$ROOT/vmlinux.bin
```

使用 Firecracker jailer 时，还会在 jailer chroot 下 bind mount runtime 所需的 kernel、rootfs、snapshot、metrics 和 API socket。删除 sandbox 前需要先解除这些 mount；否则目录删除可能失败或留下 busy mount。

排查时不要只看数据库。sandbox 目录、shim socket、runtime pid、network namespace 和 host route 都可能保留独立状态。

