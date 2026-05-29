# NovitaBox

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

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

## 🚀 Quick Start

### 1. Start the NovitaBox Server

Ensure your local machine has KVM (Linux) or equivalent virtualization features enabled.

```bash
# Clone the repository
git clone https://github.com/novitalabs/NovitaBox.git
cd NovitaBox

# Build and run the service (defaults to port 8080)
make build
./novita-box server --port 8080
```

### 2. Connect Your Agent (Novita Sandbox SDK)

Install the Novita Sandbox SDK:

```bash
pip install novita-sandbox
```

Point it at your local NovitaBox instance and run an agent:

```python
import os
from novita_sandbox import Sandbox

# Point the SDK at your local NovitaBox instance
os.environ["NOVITA_SANDBOX_URL"] = "http://localhost:8080"
os.environ["NOVITA_API_KEY"] = "NovitaBox_local_development"  # any non-empty string for local use

# Spawn a sandbox
print("Spawning a secure sandbox locally...")
sbx = Sandbox(template="base")

# Run a command
result = sbx.commands.run("echo 'Hello from NovitaBox!'")
print(result.stdout)  # → Hello from NovitaBox!

# Clean up
sbx.close()
```

The same code runs against **Novita Sandbox Cloud** by removing the `NOVITA_SANDBOX_URL` override — no other changes needed.

### Already on E2B SDK?

NovitaBox is API-compatible with E2B SDK. Point `E2B_API_URL` to your NovitaBox instance (and provide any non-empty string as `E2B_API_KEY` to satisfy the SDK's local check) — your existing E2B code works as-is, no other changes needed.

```bash
export E2B_API_URL=http://localhost:8080
export E2B_API_KEY=NovitaBox_local_development
```

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

> Detailed architecture and design rationale: see `docs/architecture.md` (TODO).

---

## 🛠️ Roadmap

### Core Capabilities

- [ ] On-demand memory loading via `uffd` (userfaultfd) and memory ballooning
- [ ] Faster Copy-on-Write (CoW) volume provisioning for storage virtualization
- [ ] Single-host multi-tenant resource constraints (CPU/memory cgroups) and network traffic shaping

### Platform Support

- [ ] **Linux (amd64)** — Native KVM virtualization with optimized system performance
- [ ] **macOS (arm64)** — Apple Silicon support via the native Hypervisor.framework
- [ ] **Windows (amd64 via WSL2)** — Seamless development workflow inside WSL2

> Replace `[ ]` with `[x]` for items already shipped before publishing.

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

