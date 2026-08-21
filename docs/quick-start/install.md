# Installation

This guide covers one-command release installs and source-based installs.

## Recommended: Release Install

The release installer downloads prebuilt NovitaBox components for the current operating system and architecture.

### Linux

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/install-release.sh | RELEASE_VERSION=<release-version> sudo -E bash
```

Linux defaults:

```text
ROOT_DIR=/data/novitabox
IMAGE_PATH=/data/novitabox.img
IMAGE_SIZE=50G
DOMAIN=novitabox.localhost
ENABLE_DNS=1
ENABLE_CADDY=1
```

### macOS with Lima

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/install-release.sh | RELEASE_VERSION=<release-version> bash
```

On macOS, the installer downloads:

```text
novitabox-darwin-<arch>.tar.gz  # host boxctl and tools
novitabox-linux-<arch>.tar.gz   # services installed inside the Lima VM
```

Homebrew and Lima are installed automatically when missing. macOS host DNS and proxy are disabled by default; enable them with:

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/install-release.sh | \
  RELEASE_VERSION=<release-version> ENABLE_MAC_DNS=1 ENABLE_MAC_PROXY=1 bash
```

### Runtime Assets

`firecracker` and `vmlinux.bin` are downloaded from the stable runtime asset release by default:

```text
RUNTIME_ASSET_VERSION=v0.0.1
```

Override only when publishing new runtime assets:

```bash
RELEASE_VERSION=<release-version> RUNTIME_ASSET_VERSION=<runtime-asset-version> scripts/install-release.sh
```

gVisor support requires a `runsc` binary. The release installer can download it from the release assets when
`RUNSC_VERSION` is set; otherwise put it at `$ROOT_DIR/runsc` or start `boxlet`/`boxshim` with `--runsc-bin`.

NVIDIA GPU support for gVisor additionally requires the NVIDIA container toolkit on the host and a CDI spec:

```bash
sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
```

### Uninstall

Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/uninstall-linux.sh | sudo -E FORCE=1 bash
```

macOS with Lima:

```bash
curl -fsSL https://raw.githubusercontent.com/novitalabs/NovitaBox/main/scripts/uninstall-macos-lima.sh | FORCE=1 bash
```

## Requirements

- Linux x86_64 or arm64
- KVM available at `/dev/kvm` for Firecracker and Cloud Hypervisor
- TUN/TAP available at `/dev/net/tun` for Firecracker tap networking
- Docker for building templates from Docker images
- Btrfs or another reflink-capable filesystem for `$ROOT`
- `runsc` for gVisor sandboxes
- NVIDIA driver, `nvidia-ctk`, and `nvidia-cdi-hook` for gVisor GPU sandboxes

To run a gVisor sandbox from an OverlayBD image, install the additional node-side
dependencies described in [gVisor with OverlayBD](./overlaybd.md): `containerd`,
`overlaybd-tcmu`, `overlaybd-snapshotter`, the OverlayBD extended `ctr`, and a
Linux kernel with TCMU, configfs, and OverlayFS support. OverlayBD is an optional
rootfs provider; the regular image/template flows do not require it.

The installer uses Btrfs by default.

## Source Install

From the repository root:

```bash
sudo -E scripts/install-linux.sh
```

Default values:

```text
ROOT_DIR=/data/novitabox
IMAGE_PATH=/data/novitabox.img
IMAGE_SIZE=50G
DOMAIN=novitabox.localhost
ENABLE_DNS=1
ENABLE_CADDY=1
```

Common overrides:

```bash
ROOT_DIR=/data/novitabox \
IMAGE_PATH=/data/novitabox.img \
IMAGE_SIZE=100G \
DOMAIN=novitabox.localhost \
sudo -E scripts/install-linux.sh
```

Use already downloaded runtime assets:

```bash
FIRECRACKER_PATH=/root/novitabox/firecracker \
KERNEL_PATH=/root/novitabox/vmlinux.bin \
sudo -E scripts/install-linux.sh
```

Skip Caddy:

```bash
ENABLE_CADDY=0 sudo -E scripts/install-linux.sh
```

Skip DNS:

```bash
ENABLE_DNS=0 sudo -E scripts/install-linux.sh
```

## What the Installer Does

The installer:

- installs system packages
- installs Go when needed
- creates a Btrfs image and mounts it at `$ROOT_DIR`
- verifies reflink support
- checks KVM and TUN/TAP for MicroVM runtimes
- enables IPv4 forwarding
- builds all NovitaBox components
- installs binaries into `$ROOT_DIR`
- installs Firecracker and guest kernel
- uses `$ROOT_DIR/runsc` for gVisor when present
- writes systemd services
- optionally configures dnsmasq
- optionally configures Caddy with local TLS

Installed binaries:

```text
$ROOT_DIR/boxapi
$ROOT_DIR/boxctl
$ROOT_DIR/boxd
$ROOT_DIR/boxlet
$ROOT_DIR/boxproxy
$ROOT_DIR/boxshim
$ROOT_DIR/firecracker
$ROOT_DIR/runsc        # optional, required for gVisor
$ROOT_DIR/vmlinux.bin
```

## Services

```bash
systemctl status novitabox-boxlet --no-pager
systemctl status novitabox-boxapi --no-pager
systemctl status novitabox-boxproxy --no-pager
```

Restart:

```bash
sudo systemctl restart novitabox-boxlet novitabox-boxapi novitabox-boxproxy
```

Logs:

```bash
journalctl -u novitabox-boxlet -f
journalctl -u novitabox-boxapi -f
journalctl -u novitabox-boxproxy -f
```

## Verify

```bash
curl http://127.0.0.1:8080/health
curl http://127.0.0.1:8082/healthz
```

If Caddy is enabled:

```bash
curl -k https://novitabox.localhost/health
```

List sandboxes:

```bash
/data/novitabox/boxctl sandbox list
```

## Build from Source Manually

```bash
make build-linux-amd64
# or
make build-linux-arm64
```

Copy binaries:

```bash
sudo install -m 0755 bin/linux-amd64/* /data/novitabox/
```

Start services manually:

```bash
/data/novitabox/boxlet --root /data/novitabox --addr 127.0.0.1:8081
/data/novitabox/boxapi --root /data/novitabox --addr 127.0.0.1:8080 --boxlet-addr 127.0.0.1:8081
/data/novitabox/boxproxy --root /data/novitabox --addr 127.0.0.1:8082
```

If `runsc` is not installed at `/data/novitabox/runsc`, pass it explicitly:

```bash
/data/novitabox/boxlet --root /data/novitabox --addr 127.0.0.1:8081 --runsc-bin /usr/local/bin/runsc
```

## Notes

The Firecracker full snapshot path depends on host KVM support. On some nested virtualization hosts, template snapshot creation can fail with:

```text
Failed to get KVM vcpu msr: 0x3a
```

That error is not a NovitaBox network issue. It means Firecracker could not save vCPU MSR state on the current host.

For gVisor GPU validation:

```bash
nvidia-smi
sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
/data/novitabox/boxctl runtime capabilities gvisor
```

Inside a GPU sandbox, `nvidia-smi` should list the host GPU and CUDA samples such as `/cuda-samples/vectorAdd` should complete with `Test PASSED`.
