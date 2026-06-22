## Security Model

NovitaBox uses jailer-based runtime restriction.

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
