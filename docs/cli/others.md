# Runtime CLI

Runtime commands show what the local runtime backend can do.

```bash
boxctl runtime [command]
```

## List

```bash
boxctl runtime list
```

## Show

```bash
boxctl runtime show firecracker
boxctl runtime show gvisor
```

## Capabilities

```bash
boxctl runtime capabilities firecracker
boxctl runtime capabilities gvisor
```

Capability fields describe whether the runtime supports:

- start from image
- start from template
- start from snapshot
- pause and resume
- full snapshots
- networking
- GPU
- graceful shutdown

Runtime summary:

- `firecracker`: MicroVM runtime with VM snapshot support.
- `gvisor`: `runsc` runtime with directory rootfs support and NVIDIA GPU support through `--gpu <count>`.
- `cloud-hypervisor`: runtime backend exposed through the same capability API.

## Global Flags

All `boxctl` commands support:

```bash
--api http://127.0.0.1:8080
--proxy http://127.0.0.1:8082
```

Use `--api` for lifecycle and artifact APIs. Use `--proxy` for process, shell, and sandbox traffic.
