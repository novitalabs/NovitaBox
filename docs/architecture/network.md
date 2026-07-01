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
  host_access_ip = 10.11.0.1
  guest_ip       = 169.254.0.21

Sandbox B:
  sandbox_id     = sbx-b
  host_access_ip = 10.11.0.2
  guest_ip       = 169.254.0.21
```

Network layout:

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

Default CIDRs:

```text
host_access_cidr = 10.11.0.0/16
veth_cidr        = 10.12.0.0/16
guest_ip         = 169.254.0.21
gateway_ip       = 169.254.0.22
```
