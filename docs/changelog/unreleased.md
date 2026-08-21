---
title: Unreleased changes
---

# Unreleased Changes

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

