# SDK 与本地域名

Novita Sandbox SDK 可以把 NovitaBox 作为本地后端。典型本地拓扑：

```text
https://novitabox.localhost       -> boxapi 127.0.0.1:8080
https://*.novitabox.localhost     -> boxproxy 127.0.0.1:8082
```

## 环境变量

```bash
export NOVITA_DOMAIN=novitabox.localhost
export NOVITA_API_KEY=dummy
export NOVITA_ACCESS_TOKEN=dummy
export NO_PROXY=.novitabox.localhost,localhost,127.0.0.1,::1
export REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
export NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/caddy-local.crt
```

如果语言运行时不读取系统 CA，可显式设置：

```bash
export SSL_CERT_FILE=/usr/local/share/ca-certificates/caddy-local.crt
```

## Python

```bash
pip install novita-sandbox==2.0.5
```

```python
from novita_sandbox import Sandbox

sbx = Sandbox(template="tpl-xxxxxxxxxxxxxxxxxxxx")
result = sbx.commands.run("echo hello from NovitaBox")
print(result.stdout)
sbx.close()
```

## Node CLI

```bash
npm install -g novita-sandbox-cli@2.0.5
novita-sandbox-cli sbx create tpl-xxxxxxxxxxxxxxxxxxxx
```

## 先验证底层

SDK 报错时先排除控制面和 runtime 问题：

```bash
/data/novitabox/boxctl template list
/data/novitabox/boxctl sandbox list
/data/novitabox/boxctl exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
curl -k https://novitabox.localhost/health
journalctl -u novitabox-boxapi -n 100 --no-pager
```

runtime 选择和 GPU 数量属于 NovitaBox 原生创建参数。需要 gVisor/GPU 时，先通过原生 API/boxctl 创建正确 runtime 的 template 和 sandbox，再让 SDK 连接或使用它们。

