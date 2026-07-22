## Security Model

NovitaBox uses runtime-specific isolation.

Firecracker uses jailer-based runtime restriction.

Jailer responsibilities:

- chroot filesystem isolation
- UID/GID restriction
- pid namespace isolation
- network namespace isolation
- seccomp filter
- limited runtime file visibility

Runtime should only see the files it needs:

- kernel
- rootfs.ext4
- snapfile
- memfile

gVisor uses `runsc` sandboxing and OCI isolation. gVisor sandboxes use their own pid, ipc, uts, mount, and network namespaces. When GPU is requested, NovitaBox enables `runsc --nvproxy` and injects only the requested NVIDIA device nodes, driver libraries, and CDI setup hooks into the OCI bundle.

GPU security notes:

- GPU support is NVIDIA-specific.
- The host must provide NVIDIA drivers, `nvidia-ctk`, `nvidia-cdi-hook`, and a CDI spec such as `/etc/cdi/nvidia.yaml`.
- GPU access exposes the requested `/dev/nvidia*` devices to the sandbox.
- NovitaBox sets `NVIDIA_VISIBLE_DEVICES`, `CUDA_VISIBLE_DEVICES`, and `NVIDIA_DRIVER_CAPABILITIES=compute,utility` for GPU sandboxes.
