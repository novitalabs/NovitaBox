# 本地 DNS

如果使用 Caddy 的 `novitabox.localhost` 和 wildcard sandbox 域名，所有域名都应解析到 `127.0.0.1`。

## dnsmasq

```text
address=/novitabox.localhost/127.0.0.1
address=/.novitabox.localhost/127.0.0.1
```

安装脚本在启用 DNS 时会写入对应配置。检查：

```bash
resolvectl query novitabox.localhost
resolvectl query sbx-xxxxxxxxxxxxxxxxxxxx.novitabox.localhost
getent hosts novitabox.localhost
```

## macOS

macOS + Lima 可以选择由安装脚本配置宿主机 DNS。若不启用宿主机 DNS，可在 Linux VM 内直连 `127.0.0.1:8080`，或手动将 `novitabox.localhost` 和 wildcard 域名指向本地代理。

## DNS 正常但 HTTPS 失败

DNS 解析和 TLS 信任是两件事：

```bash
curl -v --resolve novitabox.localhost:443:127.0.0.1 \
  https://novitabox.localhost/health
```

如果 `--resolve` 成功但普通域名失败，问题在 DNS；如果返回证书错误，安装 Caddy local CA；如果返回连接失败，检查 Caddy、boxapi 和 boxproxy 状态。

