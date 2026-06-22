<h1 align="center">NovitaBox</h1>

<div align="center"><b>——&nbsp;&nbsp;&nbsp;Local, Secure, Fast sandbox for AI Agents&nbsp;&nbsp;&nbsp;——</b></div>

<br />

<div align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="Apache 2.0 License"></a>
</div>

**The local edition of Novita Sandbox** — a secure, MicroVM-based execution environment for AI Agents.

NovitaBox brings Novita's production sandbox stack to your laptop, on-prem servers, or edge devices. Run AI Agents locally with millisecond startup times, zero cloud latency, and complete data privacy. Use the standard **Novita Sandbox SDK** to write your agent code once, then run it locally with NovitaBox or in production with Novita Sandbox Cloud — same code, two runtimes.

> Already using E2B SDK? NovitaBox is also API-compatible — point `E2B_API_URL` to your NovitaBox instance and your existing code works as-is.

---

## ✨ Features

- **🏠 Local-first** — Built for single-host deployment. Runs on your laptop, on-prem servers, or edge devices, with no cloud dependency at runtime.
- **🔒 Privacy by design** — Code, files, and execution traces never leave your machine. Suitable for air-gapped, regulated, or data-sensitive workloads.
- **⚡ Millisecond startup** — Powered by MicroVM technology with kernel-level isolation. Spin up sandboxes in milliseconds with minimal memory overhead.
- **🔁 Same SDK, two runtimes** — Use Novita Sandbox SDK for both NovitaBox (local) and Novita Sandbox Cloud. Code once, deploy anywhere.
- **🛠️ Full template lifecycle** — Build, publish, and manage custom sandbox image templates locally.
- **📦 Single binary, out-of-the-box** — No Kubernetes, no cluster, no orchestration overhead. Single binary deployment.
- **🌐 E2B SDK compatible** — Existing E2B SDK code works without modification. Migrate by changing one endpoint.

---


## Highlights

### Multi-Runtime Sandbox Platform

NovitaBox is built around a runtime abstraction instead of being tied to one backend.

Currently supported runtimes:

- Firecracker
- Cloud Hypervisor

Runtime behavior is capability-driven. If a runtime does not support a feature, such as pause/resume with GPU passthrough, NovitaBox can degrade the API behavior explicitly instead of failing unpredictably.

### Fixed Guest IP Networking

Every sandbox can see the same internal guest IP:

```text
guest_ip:   10.88.0.2
gateway_ip: 10.88.0.1
```

The guest IP is not used as the global network identity. NovitaBox routes traffic by sandbox identity:

```text
sandbox_id -> host_access_ip -> guest_ip
```

This allows templates, pause state, and resumed sandboxes to keep stable guest networking without rewriting network configuration inside the sandbox.

### Per-Sandbox Shim

Each sandbox has a dedicated `boxshim` process.

`boxshim` is the parent process of the runtime and owns the actual runtime state. If NovitaBox restarts or upgrades, running sandboxes can survive because their runtime process is owned by the shim, not by the API server.

Recovery flow:

```text
novitabox restart
  -> scan sandbox shim sockets
  -> reconnect to boxshim
  -> query runtime state
  -> reconcile persisted state
```

### Clear Image, Template, and Snapshot State Flow

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

This makes it clear which artifacts are portable, which artifacts are optimized for fast startup, and which state belongs to a running sandbox.


## 🚀 Quick Start

### 1. Start the NovitaBox Server

Ensure your local machine has KVM (Linux) or equivalent virtualization features enabled.

```bash
# Clone the repository
git clone https://github.com/novitalabs/NovitaBox.git
cd NovitaBox

# Build all components
make build

# Run services
./novita-box api --port 8080
./novita-box boxlet --port 8081
./novita-box proxy --port 8082
```

## Cli

#### Install the Novita Sandbox Cli

```bash

```


## SDK

#### Install the Novita Sandbox SDK:

```bash
pip install novita-sandbox
```

#### Launch a sandbox

see 


#### 

```bash
export NOVITA_API_URL=http://localhost:8080
export NOVITA_API_KEY=$LOCAL_API_KEY
```

#### Run code or command in sandbox

```python
import os
from novita_sandbox import Sandbox

# point the SDK at your local NovitaBox instance
os.environ["NOVITA_SANDBOX_URL"] = "http://localhost:8080" os.environ["NOVITA_API_KEY"] = "NovitaBox_local_development"  # any non-empty string for local use

# spawn a sandbox
print("Spawning a secure sandbox locally...")
sbx = Sandbox(template="base")

# run a command
result = sbx.commands.run("echo 'Hello from NovitaBox!'")
print(result.stdout)  # → Hello from NovitaBox!

# clean up
sbx.close()
```


The same code runs against Novita Sandbox Cloud by removing the NOVITA_SANDBOX_URL override — no other changes needed.

---

## 🔄 Supported API Matrix

| Feature / Method | Status | Description |
|---|---|---|
| `Sandbox.create()` | ✅ | Spawns a local MicroVM instance in milliseconds |
| `Sandbox.pause()` | ✅ | Suspends the instance with a memory snapshot, freeing host CPU |
| `Sandbox.resume()` | ✅ | Instantly resumes execution from the saved snapshot |
| `Sandbox.close()` | ✅ | Destroys the instance and cleans up network/storage |
| `Template.build()` | ✅ | Compiles custom sandbox templates from local Dockerfiles or RootFS |
| `commands.run()` | ✅ | Executes shell commands inside the sandbox |
| `filesystem.read/write/list/remove` | ✅ | Filesystem operations inside the sandbox |

> Replace ✅/⚠️/🚧 placeholders with the actual implementation status before publishing.

---

## 🏗️ Architecture

NovitaBox runs as a single process on the host and routes Novita Sandbox SDK calls to MicroVM instances managed locally:

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

> Detailed architecture and design rationale: see `docs/overview.md`.


---
## Documentation



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

## 📄 License

Distributed under the **Apache 2.0 License**. See [`LICENSE`](LICENSE) for the full text.

---

## 🔗 Related

- **[Novita Sandbox SDK](https://github.com/novitalabs/novita-sandbox-sdk)** — The unified SDK for both NovitaBox and Novita Cloud
- **[Novita Sandbox Cloud](https://novita.ai/sandbox)** — Production-grade managed sandbox service
- **[ComputeSDK Provider](https://github.com/computesdk/computesdk)** — Use NovitaBox / Novita Cloud through ComputeSDK's unified provider interface
