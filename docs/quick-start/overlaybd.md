# gVisor with OverlayBD

This guide describes the node-side dependencies and deployment procedure for running a NovitaBox gVisor sandbox from a pre-converted OverlayBD image.

The current implementation uses the existing containerd integration. NovitaBox does not implement an OverlayBD snapshotter daemon and does not use containerd to create containers or tasks.

```text
boxlet
  ├─ /opt/overlaybd/snapshotter/ctr rpull
  ├─ containerd client → /run/containerd/containerd.sock
  └─ SnapshotService("overlaybd")
                         │
                         ▼
              overlaybd-snapshotter
              /run/overlaybd-snapshotter/overlaybd.sock
                         │
                         ▼
                  overlaybd-tcmu
                         │
                         ▼
                 TCMU virtual block device
                         │
                         ▼
                  ext4 + Linux OverlayFS
                         │
                         ▼
                 sandboxes/<id>/rootfs
                         │
                         ▼
                         runsc
```

The runtime node must provide:

- Linux with TCMU and OverlayFS support;
- `containerd` and its Unix socket;
- `overlaybd-tcmu` and its OverlayBD runtime configuration;
- `overlaybd-snapshotter` and its Unix socket;
- the OverlayBD extended `ctr` with the `rpull` command;
- a registry or local cache containing pre-converted OverlayBD images;
- NovitaBox `boxlet`, `boxshim`, `runsc`, and `boxd`.

## Component responsibilities

| Component | Role in this integration | Required at runtime |
| --- | --- | --- |
| `containerd` | image metadata, content metadata, leases, snapshot API, proxy routing | yes |
| `overlaybd-snapshotter` | implements containerd's OverlayBD snapshot API and returns mount descriptions | yes |
| `overlaybd-tcmu` | OverlayBD user-space backstore; serves virtual block devices and lazy reads | yes |
| OverlayBD extended `ctr` | pulls/prepares OverlayBD images with `rpull` | yes for uncached images |
| `overlaybd-commit`/converter | creates OverlayBD images from normal OCI images | build/publish hosts only |
| Linux `target_core_user` | TCMU kernel interface | yes |
| Linux `overlay` | final writable OverlayFS mount | yes |
| `runsc` | runs the gVisor OCI bundle | yes |

The data path after a sandbox starts does not go through containerd for every file read. containerd and the snapshotter prepare and mount the filesystem; the mounted filesystem then reads through OverlayBD/TCMU and its cache.

## 1. Kernel prerequisites

### Required kernel capabilities

The exact package names depend on the distribution, but the node needs:

- TCMU kernel support: module `target_core_user`;
- target core dependencies, normally loaded automatically: `target_core_mod` and `uio`;
- OverlayFS support: module `overlay` or a kernel built with OverlayFS;
- `configfs`, mounted at `/sys/kernel/config`;
- a filesystem supported by the converted image, normally `ext4`.

OverlayBD's upstream runtime requirement is the TCMU module. The current service units also load `overlay` because the final sandbox rootfs is an OverlayFS mount.

### Check the current host

```bash
uname -a

# TCMU and target core modules
lsmod | grep -E 'target_core_user|target_core_mod|uio' || true

# OverlayFS and filesystem support
grep -E '(^| )overlay( |$)' /proc/filesystems
grep -E '(^| )ext4( |$)' /proc/filesystems || true

# configfs and target hierarchy
mountpoint /sys/kernel/config || true
test -d /sys/kernel/config/target && echo 'TCMU configfs target: ok' || true

# device nodes, if a device is currently attached
lsblk
```

### Load modules immediately

```bash
sudo modprobe configfs
sudo modprobe target_core_mod
sudo modprobe target_core_user
sudo modprobe overlay
sudo modprobe ext4
```

`target_core_mod`, `uio`, and other target modules may be loaded automatically as dependencies. If a module is built into the kernel, `modprobe` may report that it is already available; that is fine.

Ensure configfs is mounted:

```bash
sudo mkdir -p /sys/kernel/config
mountpoint -q /sys/kernel/config || \
  sudo mount -t configfs configfs /sys/kernel/config
```

Persist module loading across reboots:

```bash
sudo tee /etc/modules-load.d/novitabox-overlaybd.conf >/dev/null <<'EOF_MODULES'
configfs
target_core_mod
target_core_user
overlay
ext4
EOF_MODULES
```

On a distribution where `configfs` is mounted by systemd automatically, keep the module file but do not add a duplicate `/etc/fstab` entry.

### Kernel failure symptoms

- `modprobe: FATAL: Module target_core_user not found`: install a kernel modules package matching `uname -r`, or use a kernel built with TCMU support.
- `cannot create ... /sys/kernel/config/target`: configfs is not mounted or `target_core_user` is not loaded.
- `unknown filesystem type overlay`: OverlayFS is missing from the kernel.
- The TCMU service starts but no block device appears: inspect `journalctl -u overlaybd-tcmu` and verify target-core modules.

## 2. Build the OverlayBD backstore

The backstore repository is `containerd/overlaybd`. It builds the native OverlayBD components, including `overlaybd-tcmu`, `overlaybd-create`, `overlaybd-commit`, and related tools.

### Build dependencies on Debian/Ubuntu

```bash
sudo apt-get update
sudo apt-get install -y \
  build-essential cmake git pkg-config automake libtool \
  libaio-dev libcurl4-openssl-dev libssl-dev \
  libnl-3-dev libnl-genl-3-dev libgflags-dev \
  libzstd-dev libext2fs-dev
```

The upstream project also documents RPM-family package names. Pin the OverlayBD commit or release used by your image conversion pipeline; do not mix an untested backstore version with production image format versions.

### Compile and install

```bash
git clone https://github.com/containerd/overlaybd.git
cd overlaybd
git submodule update --init

cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j"$(nproc)"
sudo cmake --install build
```

The standard installation prefix is:

```text
/opt/overlaybd/
```

At minimum, verify:

```bash
test -x /opt/overlaybd/bin/overlaybd-tcmu
test -x /opt/overlaybd/bin/overlaybd-create
test -x /opt/overlaybd/bin/overlaybd-commit
ls -l /etc/overlaybd/overlaybd.json
```

If your build uses a different prefix, either install the binaries under the paths used by the systemd units below or update the units and OverlayBD configuration consistently.

### Configure the backstore

The default configuration is normally installed under `/etc/overlaybd/`. The important fields for a remote-image node are:

```json
{
  "logConfig": {
    "logLevel": 1,
    "logPath": "/var/log/overlaybd.log"
  },
  "cacheConfig": {
    "cacheType": "file",
    "cacheDir": "/opt/overlaybd/registry_cache",
    "cacheSizeGB": 100
  },
  "gzipCacheConfig": {
    "enable": true,
    "cacheDir": "/opt/overlaybd/gzip_cache",
    "cacheSizeGB": 20
  },
  "credentialConfig": {
    "mode": "file",
    "path": "/opt/overlaybd/cred.json"
  }
}
```

Use the configuration schema shipped by the exact OverlayBD version you built. Cache directories must have enough space and be writable by the service. For a public registry, credentials may be omitted according to the backstore version; for a private registry, configure credentials before the first device is launched.

A file-based credential example:

```json
{
  "auths": {
    "registry.example.com": {
      "username": "REGISTRY_USER",
      "password": "REGISTRY_PASSWORD"
    }
  }
}
```

Protect it:

```bash
sudo install -o root -g root -m 0600 /path/to/cred.json /opt/overlaybd/cred.json
```

Do not commit registry credentials to this repository or put them in a public deployment archive.

### Install the TCMU service

A minimal systemd unit for the paths used by this repository is:

```ini
[Unit]
Description=OverlayBD TCMU backstore
After=network.target local-fs.target
Before=overlaybd-snapshotter.service shutdown.target

[Service]
Type=simple
ExecStartPre=/sbin/modprobe target_core_user
ExecStart=/opt/overlaybd/bin/overlaybd-tcmu
Restart=always
RestartSec=1s
KillMode=process
LimitNOFILE=1048576
LimitCORE=infinity
OOMScoreAdjust=-999

[Install]
WantedBy=multi-user.target
```

Install and start it:

```bash
sudo install -m 0644 overlaybd-tcmu.service /etc/systemd/system/overlaybd-tcmu.service
sudo systemctl daemon-reload
sudo systemctl enable --now overlaybd-tcmu.service
sudo systemctl status overlaybd-tcmu.service --no-pager
```

## 3. Build the OverlayBD snapshotter and extended ctr

The snapshotter and extended `ctr` are built from `containerd/accelerated-container-image`.

### Build requirements

Use the toolchain required by the pinned upstream branch. The current upstream build guide requires Go 1.26.x, `runc >= 1.0`, and `containerd >= 2.0.x`.

```bash
git clone https://github.com/containerd/accelerated-container-image.git
cd accelerated-container-image
make
```

The build output is generated under `bin/`. Install the runtime files:

```bash
sudo install -d /opt/overlaybd/snapshotter
sudo install -m 0755 bin/overlaybd-snapshotter /opt/overlaybd/snapshotter/overlaybd-snapshotter
sudo install -m 0755 bin/ctr /opt/overlaybd/snapshotter/ctr
```

The repository may also produce `convertor` and `overlaybd-attacher`. Keep those tools on image-build or debugging hosts unless your deployment explicitly uses them.

Verify the extended `ctr`:

```bash
/opt/overlaybd/snapshotter/ctr rpull --help
```

The help must include `rpull`. A normal distribution `ctr` is not a substitute.

### Snapshotter configuration

```bash
sudo install -d /etc/overlaybd-snapshotter
sudo tee /etc/overlaybd-snapshotter/config.json >/dev/null <<'EOF_SNAPSHOTTER'
{
  "root": "/var/lib/overlaybd/",
  "address": "/run/overlaybd-snapshotter/overlaybd.sock"
}
EOF_SNAPSHOTTER

sudo install -d /var/lib/overlaybd /run/overlaybd-snapshotter
```

Register it as a containerd proxy snapshotter. Add this block to `/etc/containerd/config.toml`:

```toml
[proxy_plugins.overlaybd]
  type = "snapshot"
  address = "/run/overlaybd-snapshotter/overlaybd.sock"
```

Do not register the same plugin twice. Validate the TOML before restarting containerd.

### Install the snapshotter service

```ini
[Unit]
Description=OverlayBD containerd snapshotter
After=network.target local-fs.target overlaybd-tcmu.service
Before=containerd.service shutdown.target
Requires=overlaybd-tcmu.service

[Service]
Type=simple
ExecStartPre=/sbin/modprobe target_core_user
ExecStartPre=/sbin/modprobe overlay
ExecStart=/opt/overlaybd/snapshotter/overlaybd-snapshotter
Restart=always
RestartSec=1s
KillMode=process
LimitNOFILE=1048576
LimitCORE=infinity
OOMScoreAdjust=-999

[Install]
WantedBy=multi-user.target
```

Install it:

```bash
sudo install -m 0644 overlaybd-snapshotter.service /etc/systemd/system/overlaybd-snapshotter.service
sudo systemctl daemon-reload
sudo systemctl enable overlaybd-tcmu.service overlaybd-snapshotter.service
```

The exact binary may read `/etc/overlaybd-snapshotter/config.json` automatically. If the pinned version requires an explicit config flag, add that flag to `ExecStart` according to its `--help` output.

## 4. Start the dependency stack

When changing containerd configuration, perform the restart during a maintenance window. Existing active mounts may continue to be visible, but new snapshot operations can fail while containerd or the snapshotter is down.

```bash
sudo systemctl restart overlaybd-tcmu.service
sudo systemctl restart overlaybd-snapshotter.service
sudo systemctl restart containerd.service
```

For a clean first installation:

```bash
sudo systemctl enable --now overlaybd-tcmu.service
sudo systemctl enable --now overlaybd-snapshotter.service
sudo systemctl enable --now containerd.service
```

Verify the dependency chain:

```bash
systemctl is-active containerd overlaybd-tcmu overlaybd-snapshotter

test -S /run/containerd/containerd.sock
test -S /run/overlaybd-snapshotter/overlaybd.sock

test -x /opt/overlaybd/bin/overlaybd-tcmu
test -x /opt/overlaybd/snapshotter/overlaybd-snapshotter
test -x /opt/overlaybd/snapshotter/ctr

ss -xlpn | grep -E 'containerd.sock|overlaybd.sock'
ctr plugins ls | grep -E 'overlaybd|snapshotter'
```

The containerd plugin should report the OverlayBD snapshotter as `ok`.

Check service logs before testing NovitaBox:

```bash
journalctl -u overlaybd-tcmu -b --no-pager -n 100
journalctl -u overlaybd-snapshotter -b --no-pager -n 100
journalctl -u containerd -b --no-pager -n 100
```

## 5. Start NovitaBox

NovitaBox uses these defaults:

```text
containerd:   /run/containerd/containerd.sock
namespace:    novitabox
snapshotter:  overlaybd
ctr:          /opt/overlaybd/snapshotter/ctr
socket check: /run/overlaybd-snapshotter/overlaybd.sock
```

No `--overlaybd` feature flag is required. The path is selected by the request's `rootfs.provider=overlaybd`.

Start boxlet with defaults:

```bash
/root/novitabox/boxlet --root /root/novitabox
```

If the node uses different paths:

```bash
/root/novitabox/boxlet \
  --root /root/novitabox \
  --overlaybd-containerd /run/containerd/containerd.sock \
  --overlaybd-namespace novitabox \
  --overlaybd-snapshotter overlaybd \
  --overlaybd-ctr /opt/overlaybd/snapshotter/ctr \
  --overlaybd-socket /run/overlaybd-snapshotter/overlaybd.sock
```

`boxapi` connects to boxlet, and `boxproxy` is required for `boxctl sandbox exec` and `shell`.

## 6. Prepare or convert an OverlayBD image

NovitaBox expects an image that is already in OverlayBD format. It does not convert a normal OCI image during sandbox creation.

The upstream accelerated-container-image repository provides an image converter. One documented containerized flow is:

```bash
git clone https://github.com/containerd/accelerated-container-image.git
cd accelerated-container-image
docker build -f Dockerfile -t overlaybd-convertor .
docker run --rm overlaybd-convertor \
  -r registry.hub.docker.com/library/ubuntu \
  -i 24.04 \
  -o 24.04_obd
```

Use the converter's help and the pinned repository documentation for registry credentials, output naming, filesystem selection, and publishing. Publish the generated image to a registry accessible by the runtime node.

For the NovitaBox test node, the known working reference is:

```text
docker.io/1135479869/overlaybd:ubuntu24.04_obd
```

The tag is `ubuntu24.04_obd`, not `ubuntu2404_obd`.

## 7. Pull and test the image before creating a sandbox

Use the extended `ctr` and the same namespace/snapshotter that boxlet uses:

```bash
/opt/overlaybd/snapshotter/ctr \
  --address /run/containerd/containerd.sock \
  --namespace novitabox \
  rpull \
  --snapshotter overlaybd \
  docker.io/1135479869/overlaybd:ubuntu24.04_obd
```

If the image is private, configure OverlayBD credentials before device creation. `/root/.docker/config.json` is not automatically a substitute for `/opt/overlaybd/cred.json` in every OverlayBD deployment.

Inspect images and snapshots:

```bash
/opt/overlaybd/snapshotter/ctr --namespace novitabox images ls
/opt/overlaybd/snapshotter/ctr --namespace novitabox snapshots --snapshotter overlaybd ls
```

## 8. Create a gVisor OverlayBD sandbox

```bash
/root/novitabox/boxctl sandbox create \
  --overlaybd-image docker.io/1135479869/overlaybd:ubuntu24.04_obd
```

The creation path is:

```text
rpull/resolution
  → committed OverlayBD snapshot
  → one active writable snapshot per sandbox
  → mount at sandboxes/<id>/rootfs
  → inject boxd
  → runsc create/start
```

Inspect the selected rootfs:

```bash
/root/novitabox/boxctl sandbox list
/root/novitabox/boxctl sandbox get <sandbox-id>
findmnt /root/novitabox/sandboxes/<sandbox-id>/rootfs
```

The API reports:

```json
{
  "rootfs": {
    "provider": "overlaybd",
    "image": "docker.io/1135479869/overlaybd:ubuntu24.04_obd",
    "digest": "sha256:...",
    "snapshotKey": "novitabox-sandbox-<sandbox-id>"
  }
}
```

## 9. Lifecycle and cleanup

Poweroff keeps the active snapshot and writable data:

```bash
boxctl sandbox poweroff <sandbox-id>
findmnt /root/novitabox/sandboxes/<sandbox-id>/rootfs || true
/opt/overlaybd/snapshotter/ctr --namespace novitabox snapshots --snapshotter overlaybd ls
```

Poweron remounts the same snapshot key:

```bash
boxctl sandbox poweron <sandbox-id>
boxctl sandbox exec <sandbox-id> /bin/sh -c 'cat /root/overlaybd-test/persist.txt'
```

Delete removes the sandbox active snapshot and lease:

```bash
boxctl sandbox delete <sandbox-id>
/opt/overlaybd/snapshotter/ctr --namespace novitabox snapshots --snapshotter overlaybd ls
ctr --namespace novitabox leases ls
```

The shared committed image snapshot is not deleted when one sandbox is deleted.

## 10. Troubleshooting

### `pull access denied` or `insufficient_scope`

Check the image reference and tag first:

```bash
/opt/overlaybd/snapshotter/ctr --namespace novitabox images ls
```

Then check registry credentials in the OverlayBD configuration. A cached image can work while a fresh node fails, because the fresh node must resolve and download both metadata and OverlayBD blobs.

### `create tap` or unrelated gVisor network errors

Those errors occur before/alongside runtime setup and are not an OverlayBD mount error. Check the boxlet network configuration and stale network namespaces separately.

### `failed to connect overlaybd snapshotter`

```bash
systemctl status overlaybd-snapshotter overlaybd-tcmu containerd --no-pager
ls -l /run/overlaybd-snapshotter/overlaybd.sock
journalctl -u overlaybd-snapshotter -u overlaybd-tcmu -u containerd -b --no-pager -n 200
```

### `no such file or directory` for `/opt/overlaybd/snapshotter/ctr`

Install the extended `ctr` built from accelerated-container-image, or override `--overlaybd-ctr` with the correct absolute path.

### `target_core_user` or configfs errors

```bash
sudo modprobe configfs target_core_mod target_core_user overlay
mountpoint /sys/kernel/config
ls -la /sys/kernel/config/target
```

### Rootfs is mounted as `overlay`, not `overlaybd`

This is expected. OverlayBD supplies the lower block-device-backed filesystem; the snapshotter returns a Linux OverlayFS mount combining the lower image with the sandbox writable layer. Verify the lowerdir in `findmnt`:

```bash
findmnt -o TARGET,FSTYPE,SOURCE,OPTIONS /root/novitabox/sandboxes/<sandbox-id>/rootfs
```

## 11. Node readiness checklist

```bash
# Kernel
lsmod | grep target_core_user
grep overlay /proc/filesystems
mountpoint /sys/kernel/config

# OverlayBD
systemctl is-active overlaybd-tcmu overlaybd-snapshotter
test -S /run/overlaybd-snapshotter/overlaybd.sock
test -x /opt/overlaybd/snapshotter/ctr

# containerd
systemctl is-active containerd
test -S /run/containerd/containerd.sock
ctr plugins ls | grep overlaybd

# NovitaBox assets
test -x /root/novitabox/boxlet
test -x /root/novitabox/boxshim
test -x /root/novitabox/runsc
test -x /root/novitabox/boxd
```
