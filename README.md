<h1 align="center">NovitaBox</h1>

<div align="center"><b>——&nbsp;&nbsp;&nbsp;Local, Secure, Fast sandbox for AI Agents&nbsp;&nbsp;&nbsp;——</b></div>

<br />

<div align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="Apache 2.0 License"></a>
</div>

**The local edition of Novita Sandbox** — a secure, MicroVM-based execution environment for AI Agents.

NovitaBox brings Novita's production sandbox stack to your laptop, on-prem servers, or edge devices. Run AI Agents
locally with millisecond startup times, zero cloud latency, and complete data privacy. Use the standard **Novita Sandbox
SDK** to write your agent code once, then run it locally with NovitaBox or in Novita production.

> Already using E2B SDK? NovitaBox is also API-compatible — point `E2B_API_URL` to your NovitaBox instance and your
> existing code works as-is.

---

## ✨ Features

- **🏠 Local-first** — Built for single-host deployment. Runs on your laptop, on-prem servers, or edge devices, with no
  cloud dependency at runtime.
- **🔒 Privacy by design** — Code, files, and execution traces never leave your machine. Suitable for air-gapped,
  regulated, or data-sensitive workloads.
- **⚡ Millisecond startup** — Powered by MicroVM technology with kernel-level isolation. Spin up sandboxes in
  milliseconds with minimal memory overhead.
- **🔁 One codebase, local to production** — Use Novita Sandbox SDK for both NovitaBox local development and Novita
  production.
- **🛠️ Full template lifecycle** — Build, publish, and manage custom sandbox image templates locally.
- **🧩 Multiple runtimes** — Supports both Firecracker and Cloud Hypervisor runtimes, so you can choose the MicroVM
  backend that fits your environment.
- **🌐 E2B SDK compatible** — Existing E2B SDK code works without modification. Migrate by changing one endpoint.

## 🚀 Quick Start

#### Prepare the filesystem

NovitaBox requires a filesystem that supports reflink copies, such as XFS or Btrfs, to store templates, snapshots, and related runtime data.

For local development, you can create a disk image, format it as XFS or Btrfs, and mount it as a loop device

```bash
truncate -s 50G /data/novitabox.img

mkfs.btrfs /data/novitabox.img
# or
mkfs.xfs /data/novitabox.img

mount -o loop /data/novitabox.img /data/novitabox
```

#### Prepare runtimes

```bash
# firecracker
https://github.com/novitalabs/NovitaBox/releases/download/v0.0.1/firecracker
# firecracker guest kernel
https://github.com/novitalabs/NovitaBox/releases/download/v0.0.1/vmlinux.bin
```

#### Start the NovitaBox Server

Ensure your local machine has KVM (Linux) or equivalent virtualization features enabled.

```bash
# clone the repository
git clone https://github.com/novitalabs/NovitaBox.git
cd NovitaBox

# build all components
make build-linux-amd64

# move all components to /data/novitabox
mv bin/linux-amd64/* /data/novitabox

# run services
./boxapi --root /data/novitabox --addr 127.0.0.1:8080 --boxlet-addr 127.0.0.1:8081
./boxlet --root /data/novitabox --addr 127.0.0.1:8081
./boxproxy --root /data/novitabox --addr 127.0.0.1:8082
```

#### DNS && proxy

```bash
sudo apt-get install -y dnsmasq caddy

sudo tee /etc/dnsmasq.d/novitabox.conf >/dev/null <<'EOF'
listen-address=127.0.0.1
bind-interfaces
address=/novitabox.localhost/127.0.0.1
address=/.novitabox.localhost/127.0.0.1
EOF
sudo systemctl restart dnsmasq

sudo tee /etc/caddy/Caddyfile >/dev/null <<'EOF'
{
  local_certs
}

novitabox.localhost {
  tls internal
  reverse_proxy 127.0.0.1:8080
}

*.novitabox.localhost {
  tls internal
  reverse_proxy 127.0.0.1:8082
}
EOF
sudo systemctl restart caddy
```

#### Build a Template

```bash
/data/novitabox/boxctl \
  --api http://127.0.0.1:8080 \
  template build my-template \
  --from-image ubuntu:22.04 \
  --run 'apt-get update' \
  --run 'apt-get install -y curl'
```

#### Launch a sandbox

```bash
TEMPLATE_ID=tpl-xxxxxxxxxxxxxxxxxxxx

/data/novitabox/boxctl \
  --api http://127.0.0.1:8080 \
  sandbox create "$TEMPLATE_ID"

SANDBOX_ID=sbx-xxxxxxxxxxxxxxxxxxxx

/data/novitabox/boxctl \
  --proxy http://127.0.0.1:8082 \
  exec -it "$SANDBOX_ID" /bin/sh
```

---


## 🏗️ Architecture

NovitaBox runs as a set of small host services and routes Novita Sandbox SDK calls to MicroVM instances managed locally:

```
┌─────────────────────────────────────────────────────────────┐
│                     Your AI Agent                           │
└─────────────────────────────────────────────────────────────┘
                          │
                          ▼ Novita Sandbox SDK
┌─────────────────────────────────────────────────────────────┐
│                   NovitaBox Server                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │ HTTP API     │  │ Template     │  │ Snapshot /       │   │
│  │ (Sandbox /   │  │ Builder      │  │ Lifecycle Mgr    │   │
│  │  Template)   │  │              │  │                  │   │
│  └──────┬───────┘  └──────┬───────┘  └────────┬─────────┘   │
│         │                 │                   │             │
│         ▼                 ▼                   ▼             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │           MicroVM Manager (KVM / HVF)               │    │
│  └────────────────────┬────────────────────────────────┘    │
│                       │                                     │
│         ┌─────────────┴─────────────┐                       │
│         ▼                           ▼                       │
│  ┌──────────────┐            ┌──────────────┐               │
│  │  Sandbox VM  │  …         │  Sandbox VM  │               │
│  │  (rootfs +   │            │  (rootfs +   │               │
│  │   envd)      │            │   envd)      │               │
│  └──────────────┘            └──────────────┘               │
└─────────────────────────────────────────────────────────────┘
```

#### Multi-Runtime Sandbox Platform

NovitaBox is built around a runtime abstraction instead of being tied to one backend.

Currently supported runtimes:

- Firecracker
- Cloud Hypervisor

Runtime behavior is capability-driven. If a runtime does not support a feature, such as pause/resume with GPU
passthrough, NovitaBox can degrade the API behavior explicitly instead of failing unpredictably.

#### Fixed Guest IP Networking

Every sandbox can see the same internal guest IP:

```text
guest_ip:   169.254.0.21
gateway_ip: 169.254.0.22
```

The guest IP is not used as the global network identity. NovitaBox routes traffic by sandbox identity:

```text
sandbox_id -> host_access_ip -> guest_ip
```

This allows templates, pause state, and resumed sandboxes to keep stable guest networking without rewriting network
configuration inside the sandbox.

#### Per-Sandbox Shim

Each sandbox has a dedicated `boxshim` process.

`boxshim` is the parent process of the runtime and owns the actual runtime state. If NovitaBox restarts or upgrades,
running sandboxes can survive because their runtime process is owned by the shim, not by the API server.

Recovery flow:

```text
novitabox restart
  -> scan sandbox shim sockets
  -> reconnect to boxshim
  -> query runtime state
  -> reconcile persisted state
```

#### Clear Image, Template, and Snapshot State Flow

NovitaBox keeps the public artifact model focused on three practical states:

- Image: portable rootfs-only artifact
- Template: fast-start artifact with rootfs, memory, and VM state
- Snapshot: state files created after a Sandbox is paused

The state flow is intentionally simple:

```text
docker image + start_cmd -> Template

Template - memfile - snapfile -> Image
Image -> sandbox -> Template

Template -> Sandbox
Sandbox -> Snapshot
```

This makes it clear which artifacts are portable, which artifacts are optimized for fast startup, and which state
belongs to a running sandbox.

> Detailed architecture and design rationale: see [`docs/architecture/overview.md`](docs/architecture/overview.md).


---

## 📄 Documentation

- **Quick start:** [Install](docs/quick-start/install.md), [boxctl](docs/quick-start/boxctl.md), [SDK](docs/quick-start/sdk.md), [CLI](docs/quick-start/cli.md), [Proxy](docs/quick-start/proxy.md), [DNS](docs/quick-start/dns.md)
- **API:** [HTTP API](docs/api/http.md), [Internal RPC](docs/api/RPC.md)
- **Architecture:** [Overview](docs/architecture/overview.md), [Components](docs/architecture/components.md), [RuntimeSpec](docs/architecture/runtime.md), [Artifact Model](docs/architecture/artifact.md), [Network](docs/architecture/network.md), [Sandbox Lifecycle](docs/architecture/lifecycle.md), [File Layout](docs/architecture/file-layout.md), [Security Model](docs/architecture/security.md)
- **CLI reference:** [Template](docs/cli/template.md), [Sandbox](docs/cli/sandbox.md), [Image](docs/cli/image.md), [Runtime and snapshot](docs/cli/others.md)
- **Changelog:** [v0.0.1](docs/changelog/v0.0.1.md)
- **Chinese docs:** [使用手册](docs/zh/usage.md), [Architecture overview](docs/zh/architecture/overview.md), [v0.0.1 changelog](docs/zh/changelog/v0.0.1.md)

---

## 🤝 Contributing

Contributions are welcome — bug reports, feature requests, and pull requests all help.

1. Fork the project
2. Create your feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Open a pull request

See `CONTRIBUTING.md` for development setup and coding conventions.

---

## 🧾 License

Distributed under the **Apache 2.0 License**. See [`LICENSE`](LICENSE) for the full text.

---

## 🔗 Related

- **[Novita Sandbox SDK](https://github.com/novitalabs/novita-sandbox-sdk)** — The unified SDK for both NovitaBox and
  Novita Cloud
- **[Novita Sandbox Cloud](https://novita.ai/sandbox)** — Production-grade managed sandbox service
- **[ComputeSDK Provider](https://github.com/computesdk/computesdk)** — Use NovitaBox / Novita Cloud through
  ComputeSDK's unified provider interface
