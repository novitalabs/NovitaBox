---
title: Unreleased changes
---

# Unreleased Changes

## gVisor OverlayBD rootfs

- Allow gVisor sandboxes to start directly from a pre-converted OverlayBD image without a template build.
- Add an OverlayBD rootfs provider backed by containerd snapshot and lease APIs.
- Use the OverlayBD extended `ctr rpull` command for lazy image preparation.
- Persist the original image reference, resolved digest, rootfs provider, and active snapshot key separately per sandbox.
- Return OverlayBD image and snapshot metadata from sandbox get/list APIs.
- Return `GET /v1/sandboxes` as a top-level array, matching the template list response shape; empty lists return `[]`.
- Preserve the OverlayBD root mount while boxshim cleans nested runsc and CDI mounts.
- Unmount on poweroff, remount on poweron, and remove the active snapshot on kill.
- Add `boxlet` OverlayBD configuration flags and `boxctl sandbox create --overlaybd-image`.

## Firecracker Balloon

- Add `BalloonSpec`, `BalloonConfig`, `BalloonStats`, and `BalloonHintingStatus` to the runtime contract.
- Configure a virtio-balloon device when Firecracker starts, with a default target of `0 MiB`.
- Enable deflate-on-OOM, balloon statistics, free-page hinting, and free-page reporting by default.
- Support live target updates, statistics polling interval updates, and one-shot free-page hinting.
- Create and configure `firecracker-metrics.json` for runtime metrics.
- Expose the feature through boxshim RPC, boxlet RPC, HTTP API, and `boxctl`.
- Return an explicit unsupported-runtime error for gVisor, stub, and unsupported drivers.
- Add Firecracker API and balloon state tests.

## Known documentation/runtime differences

- The Firecracker boxshim driver reports `balloon=true`.
- The boxlet static Firecracker capability helper still needs to synchronize that field, so the public runtime capability endpoint may temporarily report `balloon=false` even though a running Firecracker sandbox can serve balloon requests.
- The current `boxctl template create/build` commands do not expose a `--runtime` flag. Create a gVisor template through `POST /v3/templates` with `runtimeType: "gvisor"`, then start the returned v2 build.
