# 网络模型

默认配置：

```text
host_access_cidr = 10.11.0.0/16
veth_cidr        = 10.12.0.0/16
guest_ip         = 169.254.0.21
gateway_ip       = 169.254.0.22
guest_mac        = 02:FC:00:00:00:05
boxd_port        = 49983
```

每个 sandbox 获得一个 network slot。slot 同时决定：

- 唯一 host access IP。
- host/netns veth 地址对。
- host 路由。
- sandbox metadata 中的 network slot。

## 公共准备流程

boxlet 创建 network namespace 后：

1. 删除同名残留 host veth、peer veth、namespace veth 和 tap。
2. 创建 veth pair。
3. 将 peer 移入 network namespace 并改名为 `eth0`。
4. 配置 host 和 namespace 地址。
5. 启用 loopback 和 `net.ipv4.ip_forward=1`。
6. 配置 namespace 默认路由。
7. 在 host 上添加 host access IP 的 `/32` 路由。
8. 根据 runtime 类型继续配置 Firecracker 或 gVisor 网络。
9. 配置宿主机 internet masquerade。

network namespace 和接口名称会通过 sandbox ID 生成 Linux 接口安全的短名称，避免超过 15 字符限制。

## Firecracker

```text
host
  -> host veth
  -> sandbox netns eth0
  -> DNAT/SNAT
  -> tap0 gateway 169.254.0.22/30
  -> guest eth0 169.254.0.21/30
  -> boxd :49983
```

Firecracker driver 将 `tap0` 和固定 guest MAC 写入 network interface 配置。每个 MicroVM 都可以复用固定 guest IP，因为它们位于不同 network namespace。

## gVisor

```text
host
  -> host veth
  -> sandbox netns eth0（唯一 host access IP）
  -> runsc sandbox
  -> boxd 0.0.0.0:49983
```

gVisor 不创建 tap。为了兼容固定 guest 地址，boxlet 还会将 guest IP 配置到 namespace loopback；runsc 直接加入已经准备好的 network namespace。

## 出网和访问

- sandbox 出网依赖 host ip forwarding 和 iptables masquerade。
- host 访问 sandbox 通过唯一 host access IP。
- boxproxy 根据数据库中的 slot 计算访问地址。
- Firecracker 在 namespace 内使用 DNAT/SNAT 将 host access IP 映射到固定 guest IP。

## 检查命令

```bash
ip netns list
ip route show
iptables -t nat -S
ip netns exec <netns> ip addr
ip netns exec <netns> ip route
```

如果 template build 报 `Device or resource busy`，重点检查相同 namespace 内是否残留 `tap0`，以及运行中的 boxlet 是否已包含按 runtime 分流、创建前删除残留 tap 的实现。

