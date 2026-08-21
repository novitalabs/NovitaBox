# Novita Sandbox CLI

The Novita Sandbox CLI can talk to a local NovitaBox deployment through the same HTTP API surface used by the hosted service.

## Install

```bash
npm install -g novita-sandbox-cli@2.0.5
```

## Local HTTPS Endpoint

If you enabled Caddy in `scripts/install-linux.sh`, the local API is available at:

```text
https://novitabox.localhost
```

The Caddy local CA is installed at:

```text
/usr/local/share/ca-certificates/caddy-local.crt
```

## Environment

```bash
export NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/caddy-local.crt
export NO_PROXY=.novitabox.localhost,localhost,127.0.0.1,::1
export NOVITA_DOMAIN=novitabox.localhost
export NOVITA_API_KEY=dummy
export NOVITA_ACCESS_TOKEN=dummy
```

If you skipped Caddy, use `boxctl` or curl against `http://127.0.0.1:8080` directly.

## Build template

```bash
novita-sandbox-cli template build
```

Run this command from a directory containing a Novita Sandbox template project.

For quick local smoke tests without a template project, use `boxctl` instead:

```bash
/data/novitabox/boxctl template build my-template \
  --from-image ubuntu:22.04 \
  --run 'echo hello from novitabox'
```

## Create Sandbox

```bash
novita-sandbox-cli sbx create tpl-xxxxxxxxxxxxxxxxxxxx
```

The CLI opens an interactive terminal after sandbox creation when the template is compatible with the current SDK version.

## Common Commands

```bash
novita-sandbox-cli sbx create tpl-xxxxxxxxxxxxxxxxxxxx
novita-sandbox-cli sbx list
novita-sandbox-cli sbx kill sbx-xxxxxxxxxxxxxxxxxxxx
```

When debugging NovitaBox itself, prefer `boxctl` because it exposes local-only commands and can directly select `--api` and `--proxy`.

```bash
/data/novitabox/boxctl sandbox list
/data/novitabox/boxctl exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
```

Runtime selection and GPU requests are local NovitaBox features exposed by `boxctl` and the native HTTP API:

```bash
/data/novitabox/boxctl template build cuda-template --from-image nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda11.7.1-ubi8
/data/novitabox/boxctl sandbox create --template tpl-xxxxxxxxxxxxxxxxxxxx --runtime gvisor --gpu 1
```

`boxctl template build` currently uses the API default runtime, Firecracker. To create a gVisor template, use the native template API with `runtimeType: "gvisor"` before starting the build.
