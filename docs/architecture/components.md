## Components

### boxapi

`boxapi` is the HTTP control plane entrypoint.

Responsibilities:

- RESTful API
- E2B-compatible routes
- Novita native routes
- authentication and authorization
- metadata and state management
- forwarding lifecycle requests to boxlet

### boxlet

`boxlet` is the node-local agent, similar in role to kubelet.

Responsibilities:

- start and manage `boxshim`
- prepare local rootfs, memory, snapshot, and runtime files
- manage local sandbox resources
- prepare sandbox network namespace, tap, veth, routes, and NAT
- build templates
- expose node runtime capabilities

### boxshim

`boxshim` is the per-sandbox runtime supervisor.

Responsibilities:

- parent process of the runtime
- start, stop, pause, resume, reboot, and kill runtime
- expose status and capability RPC
- survive NovitaBox restart
- isolate runtime-specific details behind runtime drivers

Runtime drivers:

- Firecracker driver
- Cloud Hypervisor driver

### boxd

`boxd` is the in-sandbox guest agent.

Responsibilities:

- execute commands
- provide interactive shell sessions
- handle file operations
- expose health and metrics endpoints

### boxproxy

`boxproxy` is the data plane entrypoint.

Responsibilities:

- route exec and shell traffic to boxd
- proxy user services running inside sandboxes
- upgrade shell sessions to WebSocket
- resolve sandbox routes through sandbox identity
