## Network

NovitaBox uses a per-sandbox network namespace model. Firecracker and gVisor share the same host access CIDR and veth allocation scheme, but the runtime-facing side is different.

Each sandbox has:

- fixed runtime-facing IP for the agent
- fixed gateway IP for Firecracker tap networking
- unique host access IP on the host
- unique sandbox identity

Example:

```text
Sandbox A:
  sandbox_id     = sbx-a
  host_access_ip = 10.11.0.1
  guest_ip       = 169.254.0.21

Sandbox B:
  sandbox_id     = sbx-b
  host_access_ip = 10.11.0.2
  guest_ip       = 169.254.0.21
```

Network layout:

### Firecracker

```text
Host root netns
  |
  | route 10.11.0.1/32 -> veth-sbx-a-host -> ns-sbx-a
  | route 10.11.0.2/32 -> veth-sbx-b-host -> ns-sbx-b
  |
  +-----------------------------+       +-----------------------------+
  | ns-sbx-a                    |       | ns-sbx-b                    |
  |                             |       |                             |
  | veth-sbx-a-ns               |       | veth-sbx-b-ns               |
  | 10.12.0.3/31                |       | 10.12.0.5/31                |
  |                             |       |                             |
  | DNAT                        |       | DNAT                        |
  | 10.11.0.1 -> 169.254.0.21   |       | 10.11.0.2 -> 169.254.0.21   |
  |                             |       |                             |
  | tap0                        |       | tap0                        |
  | 169.254.0.22/30             |       | 169.254.0.22/30             |
  |     |                       |       |     |                       |
  |     v                       |       |     v                       |
  | VM-A eth0 169.254.0.21      |       | VM-B eth0 169.254.0.21      |
  +-----------------------------+       +-----------------------------+
```

The host never needs to access `169.254.0.21` directly. It accesses the unique `host_access_ip`, and the sandbox namespace translates traffic into the fixed guest IP.

### gVisor

gVisor runs `boxd` as a process in the prepared network namespace. There is no VM tap device in the data path. `boxlet` puts the unique host access IP on the namespace veth and puts the fixed guest IP on loopback for compatibility with the rest of the agent model.

```text
Host root netns
  |
  | route 10.11.0.1/32 -> vh-nb-a -> nb-a
  |
  +-----------------------------+
  | nb-a                        |
  |                             |
  | eth0                        |
  | 10.12.0.3/31                |
  | 10.11.0.1/32                |
  |                             |
  | lo                          |
  | 127.0.0.1                   |
  | 169.254.0.21/30             |
  |                             |
  | runsc sandbox + boxd        |
  | listens on 0.0.0.0:49983    |
  +-----------------------------+
```

Both runtimes use the same host access URL shape:

```text
http://<host_access_ip>:49983
```

This keeps `boxproxy`, template builds, and SDK traffic independent of the runtime implementation.

Default CIDRs:

```text
host_access_cidr = 10.11.0.0/16
veth_cidr        = 10.12.0.0/16
guest_ip         = 169.254.0.21
gateway_ip       = 169.254.0.22
```
