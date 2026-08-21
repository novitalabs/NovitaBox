# 故障排查

## 排查顺序

1. 确认 boxapi/boxlet/boxproxy 服务状态。
2. 查 API 返回的 sandbox/template/build ID。
3. 查 boxlet 日志，定位失败阶段。
4. 查 sandbox 目录中的 runtime 日志。
5. 查 shim/runtime 进程、mount、network namespace 和 route。
6. 最后再检查 SDK、DNS、Caddy 和证书。

## template build 网络错误

错误包装可能是：

```text
prepare template build network: ...
```

检查：

```bash
ip netns list
ip route show
ip -o link show
journalctl -u novitabox-boxlet -n 300 --no-pager
```

### `TUNSETIFF: Device or resource busy`

通常表示同一 network namespace 里的 `tap0` 已存在、被旧 runtime 持有，或运行中的 boxlet 仍是旧版本网络逻辑。

```bash
ip netns exec <netns> ip link show
ip netns exec <netns> ip link show tap0
```

当前代码在公共网络准备阶段会删除残留 tap，并且 gVisor 走 `prepareGVisorNamespace`，不会创建 tap。若 gVisor build 仍尝试创建 `tap0`，优先确认部署二进制是否与当前源码一致。

### `no route to host`

这经常是 runtime 已提前退出后的次生错误。继续检查：

```bash
find /data/novitabox/sandboxes -name firecracker.log -print
tail -200 /data/novitabox/sandboxes/<build-id>/firecracker.log
find /data/novitabox/sandboxes -name runsc.log -print
tail -200 /data/novitabox/sandboxes/<build-id>/runsc.log
```

## Firecracker snapshot 失败

```text
Failed to get KVM vcpu msr: 0x3a
```

表示 Firecracker 无法在当前 KVM 环境保存 vCPU MSR 状态，常见于嵌套虚拟化。它不是网络错误。可尝试：

- 换用具备完整 KVM snapshot 支持的宿主机。
- 在本地开发环境改用 gVisor。
- 确认 kernel、Firecracker 和宿主机 KVM 组合兼容。

## gVisor template build 仍走 Firecracker

当前 `boxctl template build` 没有 `--runtime` 参数。使用 CLI 直接 build 时 API 默认 runtime 是 Firecracker。gVisor template 必须先调用：

```http
POST /v3/templates
{"name":"...","runtimeType":"gvisor"}
```

再调用对应 v2 build 路由。检查 template metadata 中的 `runtimeType` 是否为 `gvisor`。

## gVisor GPU

宿主机检查：

```bash
nvidia-smi
nvidia-ctk cdi list
test -f /etc/cdi/nvidia.yaml
/data/novitabox/runsc --version
```

sandbox 检查：

```bash
boxctl sandbox exec sbx-xxx /usr/bin/nvidia-smi
boxctl sandbox exec sbx-xxx /bin/sh -c 'echo "$NVIDIA_VISIBLE_DEVICES"; echo "$LD_LIBRARY_PATH"'
```

若 `nvidia-smi` 能运行但 CUDA sample 失败，继续检查 driver/library 版本、CDI symlink、`libcuda.so.1` 和 `libnvidia-ml.so.1`。

## Balloon

### 返回不支持

Balloon 只支持 Firecracker。gVisor 会返回不支持错误。

### API 成功但内存没有下降

检查：

- guest kernel 是否加载 virtio-balloon。
- guest 是否有足够可回收页。
- target 是否超过合理范围。
- statistics 是否更新。
- `firecracker-metrics.json` 是否持续写入。

```bash
boxctl sandbox balloon get sbx-xxx
boxctl sandbox balloon stats sbx-xxx
tail -f /data/novitabox/sandboxes/sbx-xxx/firecracker-metrics.json
```

### capability 显示 false

当前 Firecracker boxshim 已声明 balloon 支持，但 boxlet 的静态 runtime capability 还未同步该字段。因此 public capability API 可能显示 false，而运行中 Firecracker sandbox 的 balloon API仍可工作。

## Exec 找不到程序

```text
executable file not found
```

进入 `/bin/sh` 检查 rootfs：

```bash
boxctl exec -it sbx-xxx /bin/sh
command -v bash
command -v sh
```

很多最小镜像没有 bash、curl、ip 或 systemd。

## HTTPS 证书

```bash
ls -l /usr/local/share/ca-certificates/caddy-local.crt
sudo update-ca-certificates
curl -k https://novitabox.localhost/health
```

Python 使用 `REQUESTS_CA_BUNDLE`/`SSL_CERT_FILE`，Node 使用 `NODE_EXTRA_CA_CERTS`。还要确保 `NO_PROXY` 包含 `.novitabox.localhost`。

## 残留资源

```bash
ps -ef | grep -E 'boxshim|firecracker|runsc'
findmnt -rn | grep /data/novitabox
ip netns list
ip -o link show | grep -E 'vh-|tap0'
ip route show | grep 10.11
```

删除残留资源属于有状态操作。先确认对应 sandbox/runtime 已停止，再按 sandbox ID 精确清理，不要批量删除所有 network namespace 或 mount。

