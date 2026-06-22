## File Layout

`$ROOT` defaults to `/var/lib`.

```text
$ROOT/novitabox/images/<image_id>/
  rootfs.ext4
  metadata

$ROOT/novitabox/templates/<template_id>/
  rootfs.ext4
  memfile
  snapfile
  metadata

$ROOT/novitabox/sandboxes/<sandbox_id>/
  shim.sock
  shim.pid
  fc.sock

$ROOT/novitabox/sandboxes/<sandbox_id>/snapshot/
  rootfs.ext4
  memfile
  snapfile
  metadata
```
