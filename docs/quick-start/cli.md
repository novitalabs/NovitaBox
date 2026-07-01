# Novita Sandbox CLI

The Novita Sandbox CLI can talk to a local NovitaBox deployment through the same HTTP API surface used by the hosted service.

## Install

```bash
npm install -g novita-sandbox-cli@2.0.5
```

## Local HTTPS Endpoint

If you enabled Caddy in `scripts/install.sh`, the local API is available at:

```text
https://novitabox.local
```

The Caddy local CA is installed at:

```text
/usr/local/share/ca-certificates/caddy-local.crt
```

## Environment

```bash
export NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/caddy-local.crt
export NO_PROXY=.novitabox.local,localhost,127.0.0.1,::1
export NOVITA_DOMAIN=novitabox.local
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
