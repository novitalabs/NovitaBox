## File Layout

`$ROOT` defaults to `/data/novitabox` in the installer.

```text
$ROOT/db/
  novitabox.db

$ROOT/images/<image_id>/
  rootfs.ext4
  metadata

$ROOT/images/<image_id>/rootfs/
  ... directory rootfs for gVisor images

$ROOT/templates/<template_id>/
  rootfs.ext4
  memfile
  snapfile
  metadata

$ROOT/templates/<template_id>/rootfs/
  ... directory rootfs for gVisor templates

$ROOT/sandboxes/<sandbox_id>/
  shim.sock
  shim.pid
  fc.sock
  firecracker.log
  kernel -> $ROOT/vmlinux.bin
  snapshot/

$ROOT/sandboxes/<sandbox_id>/bundle/
  config.json          # gVisor OCI bundle

$ROOT/sandboxes/<sandbox_id>/runsc/
  ... runsc state

$ROOT/sandboxes/<sandbox_id>/runsc.log
  ... gVisor runtime log

$ROOT/sandboxes/<sandbox_id>/rootfs/
  ... directory rootfs for gVisor sandboxes

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
$ROOT/runsc
$ROOT/vmlinux.bin
```

Firecracker artifacts use `rootfs.ext4`, `memfile`, and `snapfile`. gVisor artifacts use directory rootfs paths and do not create VM memory snapshot files.
