## Artifact Model

NovitaBox uses three artifact concepts.

### Image

An Image contains only rootfs state.

```text
Firecracker image = rootfs.ext4 + metadata
gVisor image      = rootfs directory + metadata
```

Images are portable and useful for migration.

### Template

A Template contains the fast-start state for a selected runtime.

```text
Firecracker template = rootfs.ext4 + memfile + snapfile + metadata
gVisor template      = rootfs directory + metadata
```

Firecracker templates are optimized for VM snapshot startup. gVisor templates are directory rootfs templates and do not include `memfile` or `snapfile`.

### Snapshot

A Snapshot is internal pause state bound to a sandbox.

```text
Firecracker snapshot = rootfs.ext4 + memfile + snapfile + metadata
```

Snapshot is not a user-convertible artifact. It is created by pause and consumed by resume. gVisor pause/resume snapshots are not supported yet.

## Artifact Conversion

User-facing artifact conversion is intentionally limited:

```text
docker image + start_cmd -> Template

Firecracker Template - memfile - snapfile -> Image
Image -> Firecracker Template

Template -> Sandbox
Firecracker Sandbox -> Snapshot
```

For gVisor, conversion keeps directory rootfs state and skips VM memory/snapshot files:

```text
docker image -> directory-rootfs Template
directory-rootfs Template -> directory-rootfs Image
directory-rootfs Template -> gVisor Sandbox
```
