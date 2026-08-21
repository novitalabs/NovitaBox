# Novita Sandbox SDK

The Novita Sandbox SDK can use NovitaBox as its local backend when DNS and the reverse proxy are configured.

## Install

```bash
pip install novita-sandbox==2.0.5
```

## Environment

For a Caddy-backed local deployment:

```bash
export NOVITA_DOMAIN=novitabox.localhost
export NOVITA_API_KEY=dummy
export NOVITA_ACCESS_TOKEN=dummy
export REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
export NO_PROXY=.novitabox.localhost,localhost,127.0.0.1,::1
```

If your Python HTTP stack does not use the system CA bundle, point it directly to Caddy's local CA:

```bash
export SSL_CERT_FILE=/usr/local/share/ca-certificates/caddy-local.crt
```

## Run a Command

```python
import os
from novita_sandbox import Sandbox

os.environ["NOVITA_DOMAIN"] = "novitabox.localhost"
os.environ["NOVITA_API_KEY"] = "dummy"
os.environ["NOVITA_ACCESS_TOKEN"] = "dummy"

sbx = Sandbox(template="tpl-xxxxxxxxxxxxxxxxxxxx")

result = sbx.commands.run("echo 'Hello from NovitaBox!'")
print(result.stdout)

sbx.close()
```

The same code runs in Novita production with your production API key and endpoint. No code changes required.

Runtime selection and GPU allocation are NovitaBox local control-plane options. Create those templates and sandboxes with `boxctl` or the native HTTP API, then use the SDK against the resulting template or sandbox:

```bash
/data/novitabox/boxctl template build cuda-template --from-image nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda11.7.1-ubi8
/data/novitabox/boxctl sandbox create --template tpl-xxxxxxxxxxxxxxxxxxxx --runtime gvisor --gpu 1
```

The first command creates a Firecracker template because the current `boxctl template build` command has no runtime flag. For a gVisor template, use `POST /v3/templates` with `runtimeType: "gvisor"`, then start the returned v2 build. The sandbox command above shows the valid runtime selection flag for sandbox creation.

## Verify with boxctl

When SDK calls fail, first check the underlying local state:

```bash
/data/novitabox/boxctl sandbox list
/data/novitabox/boxctl template list
journalctl -u novitabox-boxapi -n 100 --no-pager
```
