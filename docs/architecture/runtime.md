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
  -> translates RuntimeSpec into Firecracker or Cloud Hypervisor operations
```

RuntimeSpec includes:

- sandbox ID
- runtime type
- machine resources
- kernel and rootfs paths
- snapshot paths
- network configuration
- agent configuration
- jailer configuration
- labels and annotations

Runtime-specific details should live in runtime driver options rather than leaking into the upper layers.

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
- hotplug disk
- live resize CPU
- live resize memory
- graceful shutdown
- serial console
- jailer

Capabilities allow NovitaBox to degrade API behavior based on the selected runtime.