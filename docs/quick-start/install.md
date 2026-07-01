# Installation

This guide installs NovitaBox on one Linux host.

## Requirements

- Linux x86_64 or arm64
- KVM available at `/dev/kvm`
- TUN/TAP available at `/dev/net/tun`
- Docker for building templates from Docker images
- Btrfs or another reflink-capable filesystem for `$ROOT`

The installer uses Btrfs by default.

## One-Command Install

From the repository root:

```bash
sudo -E scripts/install.sh
```

Default values:

```text
ROOT_DIR=/data/novitabox
IMAGE_PATH=/data/novitabox.img
IMAGE_SIZE=50G
DOMAIN=novitabox.local
ENABLE_DNS=1
ENABLE_CADDY=1
```

Common overrides:

```bash
ROOT_DIR=/data/novitabox \
IMAGE_PATH=/data/novitabox.img \
IMAGE_SIZE=100G \
DOMAIN=novitabox.local \
sudo -E scripts/install.sh
```

Use already downloaded runtime assets:

```bash
FIRECRACKER_PATH=/root/novitabox/firecracker \
KERNEL_PATH=/root/novitabox/vmlinux.bin \
sudo -E scripts/install.sh
```

Skip Caddy:

```bash
ENABLE_CADDY=0 sudo -E scripts/install.sh
```

Skip DNS:

```bash
ENABLE_DNS=0 sudo -E scripts/install.sh
```

## What the Installer Does

The installer:

- installs system packages
- installs Go when needed
- creates a Btrfs image and mounts it at `$ROOT_DIR`
- verifies reflink support
- checks KVM and TUN/TAP
- enables IPv4 forwarding
- builds all NovitaBox components
- installs binaries into `$ROOT_DIR`
- installs Firecracker and guest kernel
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
curl -k https://novitabox.local/health
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

## Notes

The Firecracker full snapshot path depends on host KVM support. On some nested virtualization hosts, template snapshot creation can fail with:

```text
Failed to get KVM vcpu msr: 0x3a
```

That error is not a NovitaBox network issue. It means Firecracker could not save vCPU MSR state on the current host.
