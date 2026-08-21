## RuntimeSpec

`RuntimeSpec` is the internal contract between `boxlet` and `boxshim`.

It describes what kind of runtime should be started without exposing runtime-specific implementation details to boxlet.

```text
boxlet
  -> creates RuntimeSpec
  -> starts boxshim
  -> calls StartRuntime(RuntimeSpec)

boxshim
  -> selects runtime driver
  -> translates RuntimeSpec into Firecracker, gVisor, or Cloud Hypervisor operations
```

RuntimeSpec includes:

- sandbox ID
- runtime type
- machine resources
- kernel and rootfs paths
- snapshot paths when the selected runtime uses VM snapshot state
- network configuration
- agent configuration
- jailer configuration
- balloon configuration for runtimes that support virtio-balloon
- labels and annotations

Runtime-specific details should live in runtime driver options rather than leaking into the upper layers.

## Supported Runtimes

### Firecracker

Firecracker is the default MicroVM runtime. It uses:

- `rootfs.ext4`
- guest kernel
- `memfile` and `snapfile` for template startup and pause/resume
- tap networking inside a per-sandbox network namespace
- virtio-balloon with live target updates, statistics, and free-page hinting/reporting

Firecracker is the runtime to use when full VM snapshot behavior is required.

### gVisor

gVisor uses `runsc` as the low-level runtime. It uses:

- directory rootfs
- OCI bundle under the sandbox directory
- existing host network namespace prepared by `boxlet`
- optional NVIDIA GPU injection with `runsc --nvproxy`

gVisor templates are directory-rootfs templates. They do not produce Firecracker `memfile` or `snapfile` artifacts.

GPU support is enabled by setting `MachineSpec.gpu` to a positive count. NovitaBox reads NVIDIA CDI specs from standard locations such as `/etc/cdi/nvidia.yaml` and merges the device nodes, bind mounts, environment variables, and supported hooks into the OCI spec. The `update-ldcache` CDI hook is skipped under `runsc`; NovitaBox injects the NVIDIA library path and creates the required `libcuda.so.1` and `libnvidia-ml.so.1` links through CDI symlink hooks.

gVisor can also use OverlayBD as its rootfs provider. In this mode containerd and the existing OverlayBD snapshotter own image and snapshot storage, while NovitaBox continues to own the direct `runsc` lifecycle. Sandbox creation runs OverlayBD `rpull`, creates one writable active snapshot, mounts it at `sandboxes/<id>/rootfs`, injects boxd, and starts runsc. Poweroff unmounts but preserves the snapshot; poweron remounts it; kill removes it through the snapshotter API. No NovitaBox template build is involved.

### Cloud Hypervisor

Cloud Hypervisor is exposed through the same runtime abstraction. Capability fields decide which operations are available to users.

## Runtime Capabilities

Each runtime exposes capabilities:

- start from image
- start from template
- start from snapshot
- pause
- resume
- full snapshot
- diff snapshot
- GPU
- vsock
- tap networking
- hotplug disk
- live resize CPU
- live resize memory
- balloon
- graceful shutdown
- serial console
- jailer

Capabilities allow NovitaBox to degrade API behavior based on the selected runtime. For example, gVisor supports start from image/template and graceful shutdown, but does not currently support Firecracker-style pause/resume snapshots or balloon operations.

The Firecracker boxshim driver reports `balloon=true`. The boxlet's static runtime capability helper still needs to synchronize that field, so the public runtime capability endpoint may temporarily report `balloon=false` even though a running Firecracker sandbox can serve balloon requests through its boxshim.
