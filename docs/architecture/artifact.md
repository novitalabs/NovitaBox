## Artifact Model

NovitaBox uses three artifact concepts.

### Image

An Image contains only rootfs state.

```text
Image = rootfs.ext4 + metadata
```

Images are portable and useful for migration.

### Template

A Template contains rootfs, memory state, and VM snapshot state.

```text
Template = rootfs.ext4 + memfile + snapfile + metadata
```

Templates are optimized for fast sandbox startup.

### Snapshot

A Snapshot is internal pause state bound to a sandbox.

```text
Snapshot = rootfs.ext4 + memfile + snapfile + metadata
```

Snapshot is not a user-convertible artifact. It is created by pause and consumed by resume.

## Artifact Conversion

User-facing artifact conversion is intentionally limited:

```text
docker image + start_cmd -> Template

Template - memfile - snapfile -> Image
Image -> sandbox -> Template

Template -> Sandbox
Sandbox -> Snapshot
```
