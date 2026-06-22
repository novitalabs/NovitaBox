## Network

NovitaBox uses a fixed guest IP model.

Each sandbox has:

- fixed guest IP inside the VM
- fixed gateway IP inside its namespace
- unique host access IP on the host
- unique sandbox identity

Example:

```text
Sandbox A:
  sandbox_id     = sbx-a
  host_access_ip = 100.96.0.10
  guest_ip       = 10.88.0.2

Sandbox B:
  sandbox_id     = sbx-b
  host_access_ip = 100.96.0.11
  guest_ip       = 10.88.0.2
```

Network layout:

```text
Host root netns
  |
  | route 100.96.0.10/32 -> veth-sbx-a-host -> ns-sbx-a
  | route 100.96.0.11/32 -> veth-sbx-b-host -> ns-sbx-b
  |
  +-----------------------------+       +-----------------------------+
  | ns-sbx-a                    |       | ns-sbx-b                    |
  |                             |       |                             |
  | veth-sbx-a-ns               |       | veth-sbx-b-ns               |
  | 169.254.100.2/30            |       | 169.254.101.2/30            |
  |                             |       |                             |
  | DNAT                        |       | DNAT                        |
  | 100.96.0.10 -> 10.88.0.2    |       | 100.96.0.11 -> 10.88.0.2    |
  |                             |       |                             |
  | tap0                        |       | tap0                        |
  | 10.88.0.1/30                |       | 10.88.0.1/30                |
  |     |                       |       |     |                       |
  |     v                       |       |     v                       |
  | VM-A eth0 10.88.0.2         |       | VM-B eth0 10.88.0.2         |
  +-----------------------------+       +-----------------------------+
```

The host never needs to access `10.88.0.2` directly. It accesses the unique `host_access_ip`, and the sandbox namespace translates traffic into the fixed guest IP.
