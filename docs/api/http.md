## API Overview

NovitaBox exposes compatibility routes and local native routes. The default local API endpoint is:

```text
http://127.0.0.1:8080
```

When Caddy is enabled:

```text
https://novitabox.localhost
```

## Health

```bash
curl http://127.0.0.1:8080/health
curl http://127.0.0.1:8080/healthz
```

## Templates

Create a template record:

```bash
curl -sS -X POST http://127.0.0.1:8080/v3/templates \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-template"}'
```

Create a gVisor template record:

```bash
curl -sS -X POST http://127.0.0.1:8080/v3/templates \
  -H 'Content-Type: application/json' \
  -d '{"name":"cuda-template","runtimeType":"gvisor"}'
```

Start a build:

```bash
curl -sS -X POST http://127.0.0.1:8080/v2/templates/tpl-xxxxxxxxxxxxxxxxxxxx/builds/<build_id> \
  -H 'Content-Type: application/json' \
  -d '{
    "fromImage": "ubuntu:22.04",
    "steps": [
      {"type": "RUN", "args": ["echo hello from novitabox"]}
    ]
  }'
```

Get build status:

```bash
curl -sS http://127.0.0.1:8080/v2/templates/tpl-xxxxxxxxxxxxxxxxxxxx/builds/<build_id>/status
```

List templates:

```bash
curl -sS http://127.0.0.1:8080/templates
```

Get a template:

```bash
curl -sS http://127.0.0.1:8080/templates/tpl-xxxxxxxxxxxxxxxxxxxx
```

Delete a template:

```bash
curl -i -X DELETE http://127.0.0.1:8080/templates/tpl-xxxxxxxxxxxxxxxxxxxx
```

## Sandboxes

Create a sandbox from a template:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes \
  -H 'Content-Type: application/json' \
  -d '{"templateID":"tpl-xxxxxxxxxxxxxxxxxxxx"}'
```

Create a gVisor sandbox with one NVIDIA GPU:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes \
  -H 'Content-Type: application/json' \
  -d '{"templateID":"tpl-xxxxxxxxxxxxxxxxxxxx","runtime_type":"gvisor","gpu":1}'
```

Create from an image:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes \
  -H 'Content-Type: application/json' \
  -d '{"imageID":"img-xxxxxxxxxxxxxxxxxxxx"}'
```

List sandboxes:

```bash
curl -sS http://127.0.0.1:8080/v1/sandboxes
```

Get sandbox info:

```bash
curl -sS http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx
```

Pause and resume:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/pause
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/resume
```

Power lifecycle:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/poweroff
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/poweron
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/reboot
```

## Firecracker Balloon

Firecracker sandboxes create a balloon device with an initial target of `0 MiB`. Balloon statistics, `deflate_on_oom`, free-page hinting, and free-page reporting are enabled by default. The device is available for live memory reclamation without reducing guest memory at sandbox creation time. `amountMiB` is the target amount of memory to reclaim from the guest.

Set and inspect the target:

```bash
curl -sS -X PATCH http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/balloon \
  -H 'Content-Type: application/json' \
  -d '{"amountMiB":1024}'

curl -sS http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/balloon
```

Read balloon statistics and change the polling interval:

```bash
curl -sS http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/balloon/statistics

curl -sS -X PATCH http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/balloon/statistics \
  -H 'Content-Type: application/json' \
  -d '{"statsPollingIntervalS":1}'
```

Start, inspect, and stop free-page hinting:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/balloon/hinting/start \
  -H 'Content-Type: application/json' \
  -d '{"acknowledgeOnStop":true}'
curl -sS http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/balloon/hinting
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx/balloon/hinting/stop
```

Hinting is a one-shot run, not a periodic task. Its status exposes Firecracker's `hostCmd` and optional `guestCmd`; command `0` is stopped, `1` is completed, and values greater than `1` identify a hinting run.

Balloon endpoints return an unsupported/conflict response for gVisor and other runtimes. The guest kernel must include the virtio-balloon driver for reclamation, statistics, and free-page reporting to have an effect. Statistics include swap, fault, free/available memory, cache, HugeTLB, OOM, allocation stall, scan, and reclaim counters when the guest kernel exposes them.

Delete:

```bash
curl -i -X DELETE http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx
```

## Images

Create an image from a template:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/images \
  -H 'Content-Type: application/json' \
  -d '{"templateID":"tpl-xxxxxxxxxxxxxxxxxxxx","imageID":"img-xxxxxxxxxxxxxxxxxxxx"}'
```

List images:

```bash
curl -sS http://127.0.0.1:8080/v1/images
```

Get an image:

```bash
curl -sS http://127.0.0.1:8080/v1/images/img-xxxxxxxxxxxxxxxxxxxx
```

Delete an image:

```bash
curl -i -X DELETE http://127.0.0.1:8080/v1/images/img-xxxxxxxxxxxxxxxxxxxx
```

## Runtimes

```bash
curl -sS http://127.0.0.1:8080/v1/runtimes
curl -sS http://127.0.0.1:8080/v1/runtimes/firecracker
curl -sS http://127.0.0.1:8080/v1/runtimes/firecracker/capabilities
curl -sS http://127.0.0.1:8080/v1/runtimes/gvisor
curl -sS http://127.0.0.1:8080/v1/runtimes/gvisor/capabilities
```

Runtime names accepted by the API include `firecracker`, `gvisor`, and `cloud-hypervisor`. The compatible template API uses `runtimeType`; sandbox creation uses `runtime_type`.

## Route Summary

### Compatible API

```http
POST   /v1/sandboxes
GET    /v1/sandboxes
GET    /v1/sandboxes/{sandbox_id}
DELETE /v1/sandboxes/{sandbox_id}

POST   /v1/sandboxes/{sandbox_id}/pause
POST   /v1/sandboxes/{sandbox_id}/resume

POST   /v1/sandboxes/{sandbox_id}/timeout
POST   /v1/sandboxes/{sandbox_id}/refresh

POST   /v1/templates
GET    /v1/templates
GET    /v1/templates/{template_id}
DELETE /v1/templates/{template_id}
```

### Native API

```http
POST   /v1/sandboxes/{sandbox_id}/poweroff
POST   /v1/sandboxes/{sandbox_id}/poweron
POST   /v1/sandboxes/{sandbox_id}/reboot
GET    /v1/sandboxes/{sandbox_id}/balloon
PATCH  /v1/sandboxes/{sandbox_id}/balloon
GET    /v1/sandboxes/{sandbox_id}/balloon/statistics
PATCH  /v1/sandboxes/{sandbox_id}/balloon/statistics
GET    /v1/sandboxes/{sandbox_id}/balloon/hinting
POST   /v1/sandboxes/{sandbox_id}/balloon/hinting/start
POST   /v1/sandboxes/{sandbox_id}/balloon/hinting/stop

POST   /v1/templates/convert

POST   /v1/images
GET    /v1/images
GET    /v1/images/{image_id}
DELETE /v1/images/{image_id}

GET    /v1/runtimes
GET    /v1/runtimes/{runtime_type}
GET    /v1/runtimes/{runtime_type}/capabilities
```
