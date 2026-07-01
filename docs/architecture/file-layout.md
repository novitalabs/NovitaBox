## File Layout

`$ROOT` defaults to `/data/novitabox` in the installer.

```text
$ROOT/db/
  novitabox.db

$ROOT/images/<image_id>/
  rootfs.ext4
  metadata

$ROOT/templates/<template_id>/
  rootfs.ext4
  memfile
  snapfile
  metadata

$ROOT/sandboxes/<sandbox_id>/
  shim.sock
  shim.pid
  fc.sock
  firecracker.log
  kernel -> $ROOT/vmlinux.bin
  snapshot/

$ROOT/sandboxes/<sandbox_id>/snapshot/
  rootfs.ext4
  memfile
  snapfile
  metadata

$ROOT/boxapi
$ROOT/boxctl
$ROOT/boxd
$ROOT/boxlet
$ROOT/boxproxy
$ROOT/boxshim
$ROOT/firecracker
$ROOT/vmlinux.bin
```
