# Caddy 反向代理

NovitaBox 有两个本地 HTTP 服务：

```text
boxapi   127.0.0.1:8080   控制面和 artifact API
boxproxy 127.0.0.1:8082   exec、shell 和 sandbox 数据面
```

推荐用 Caddy 提供本地 CA、API 域名和 wildcard sandbox 域名。

## Caddyfile

```caddyfile
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
```

安装并重启：

```bash
sudo apt install -y caddy
sudo systemctl restart caddy
sudo systemctl status caddy --no-pager
```

安装 Caddy 本地 CA：

```bash
sudo cp /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt \
  /usr/local/share/ca-certificates/caddy-local.crt
sudo update-ca-certificates
```

## 路由

```text
https://novitabox.localhost
  -> 127.0.0.1:8080

https://<sandbox-or-port>.novitabox.localhost
  -> 127.0.0.1:8082
```

boxproxy 会从 host/header 中解析 sandbox identity，再从本地数据库查询 host access IP。

## 检查

```bash
curl -k https://novitabox.localhost/health
curl http://127.0.0.1:8082/healthz
```

客户端仍然证书错误时，检查系统 CA、`REQUESTS_CA_BUNDLE`、`SSL_CERT_FILE`、`NODE_EXTRA_CA_CERTS` 和 `NO_PROXY`。

