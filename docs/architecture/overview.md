## Architecture

NovitaBox is split into control plane, node agent, runtime shim, guest agent, and data proxy components.

```text
Client / SDK / CLI
        |
        | REST / WebSocket
        v
    novitabox
        |
        +-- api
        |     - E2B-compatible API
        |     - Novita native API
        |     - auth and metadata
        |     - sandbox/template/image state management
        |
        +-- boxlet
        |     - node-local agent
        |     - local resource management
        |     - starts boxshim
        |     - template build
        |
        +-- proxy
              - exec and shell proxy
              - sandbox service proxy
              - WebSocket upgrade

boxshim
        - one shim per sandbox
        - runtime parent process
        - exposes RPC over Unix socket/ttrpc
        - manages Firecracker, gVisor, or Cloud Hypervisor

boxd
        - agent inside sandbox
        - exec
        - shell
        - file operations
```

### Runtime Families

NovitaBox supports MicroVM and gVisor container runtimes through the same `RuntimeSpec` contract.

- Firecracker uses a VM rootfs, guest kernel, tap device, and VM snapshot artifacts.
- gVisor uses a directory rootfs, OCI bundle, `runsc`, and the prepared sandbox network namespace directly.
- Cloud Hypervisor is exposed through the same runtime capability model.

NVIDIA GPU support is implemented on the gVisor path. `boxshim` enables `runsc --nvproxy`, reads NVIDIA CDI specs, injects GPU device nodes and driver libraries, and keeps runtime-specific NVIDIA setup inside the gVisor OCI bundle.


### Copy-on-Write Memory and Storage Direction

NovitaBox is designed to support copy-on-write memory and storage flows.

Example memory flow:

```text
template memory-ranges
    |
    | mmap MAP_PRIVATE
    | lazy restore
    v
running VM memory
    |
    | guest writes cause CoW
    v
template-backed pages + private CoW pages
    |
    | pause snapshot
    v
pausevm/<sandbox-id>/memory-ranges
    |
    | resume
    | mmap MAP_PRIVATE
    v
resumed VM memory
```

For storage, btrfs or XFS is recommended to support reflink-based copies. Firecracker artifacts are file based; gVisor artifacts are directory-rootfs based.
