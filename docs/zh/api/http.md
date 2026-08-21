# HTTP API

`boxapi` 默认监听 `127.0.0.1:8080`。当前实现同时保留兼容路由、v1 原生路由、v2 build 路由和 v3 template 创建路由。

## 健康检查

```bash
curl -sS http://127.0.0.1:8080/health
curl -sS http://127.0.0.1:8080/healthz
```

## Template

### 创建 template 记录和 build ID

```bash
curl -sS -X POST http://127.0.0.1:8080/v3/templates \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"python-dev",
    "templateID":"tpl-xxxxxxxxxxxxxxxxxxxx",
    "cpuCount":2,
    "memoryMB":2048,
    "runtimeType":"firecracker"
  }'
```

`runtimeType` 支持 `firecracker`、`gvisor` 和 `cloud-hypervisor`。省略时默认 `firecracker`。返回值包含 `templateID` 和 `buildID`。

### 启动 build

```bash
curl -sS -X POST \
  http://127.0.0.1:8080/v2/templates/tpl-xxxxxxxxxxxxxxxxxxxx/builds/build-id \
  -H 'Content-Type: application/json' \
  -d '{
    "fromImage":"ubuntu:22.04",
    "startCmd":"/novitabox/init",
    "readyCmd":"curl -fsS http://127.0.0.1:49983/health",
    "steps":[
      {"type":"RUN","args":["apt-get update && apt-get install -y python3"]},
      {"type":"EXEC","args":["python3","--version"]}
    ]
  }'
```

- `RUN` 使用 shell 执行一个命令字符串。
- `EXEC` 直接使用参数数组执行程序。
- `fromImage` 和 `fromTemplate` 用于指定 build 来源。
- Firecracker build 最终生成 `rootfs.ext4`、`memfile` 和 `snapfile`。
- gVisor build 最终生成 directory rootfs，不创建 VM snapshot 文件。

查询状态：

```bash
curl -sS \
  http://127.0.0.1:8080/v2/templates/tpl-xxxxxxxxxxxxxxxxxxxx/builds/build-id/status
```

查询和删除：

```bash
curl -sS http://127.0.0.1:8080/templates
curl -sS http://127.0.0.1:8080/templates/tpl-xxxxxxxxxxxxxxxxxxxx
curl -i -X DELETE http://127.0.0.1:8080/templates/tpl-xxxxxxxxxxxxxxxxxxxx
```

### 创建 gVisor template

当前 `boxctl template build` 没有 `--runtime` 参数。需要通过 v3 API 创建带 `runtimeType: "gvisor"` 的记录，然后调用 v2 build：

```bash
create_response=$(curl -sS -X POST http://127.0.0.1:8080/v3/templates \
  -H 'Content-Type: application/json' \
  -d '{"name":"cuda-sample","runtimeType":"gvisor"}')

echo "$create_response"
```

取得返回的 `templateID`、`buildID` 后执行：

```bash
curl -sS -X POST \
  http://127.0.0.1:8080/v2/templates/<templateID>/builds/<buildID> \
  -H 'Content-Type: application/json' \
  -d '{"fromImage":"nvcr.io/nvidia/k8s/cuda-sample:vectoradd-cuda11.7.1-ubi8"}'
```

## Sandbox

从 template 创建：

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes \
  -H 'Content-Type: application/json' \
  -d '{"templateID":"tpl-xxxxxxxxxxxxxxxxxxxx","runtime_type":"firecracker"}'
```

从 image 创建：

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes \
  -H 'Content-Type: application/json' \
  -d '{"image_id":"img-xxxxxxxxxxxxxxxxxxxx","runtime_type":"gvisor","gpu":1}'
```

查询：

```bash
curl -sS http://127.0.0.1:8080/v1/sandboxes
curl -sS http://127.0.0.1:8080/v1/sandboxes/sbx-xxxxxxxxxxxxxxxxxxxx
```

生命周期：

```bash
curl -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/pause
curl -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/resume
curl -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/poweroff
curl -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/poweron
curl -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/reboot
curl -X DELETE http://127.0.0.1:8080/v1/sandboxes/sbx-xxx
```

pause/resume 是 runtime capability。Firecracker 支持 VM snapshot pause/resume；gVisor 当前不支持 Firecracker 风格的 snapshot pause/resume。

## Firecracker Balloon

Firecracker sandbox 创建时会配置 virtio-balloon。默认 target 为 `0 MiB`，并启用：

- `deflateOnOOM`
- `statsPollingIntervalS = 1`
- free-page hinting
- free-page reporting

设置回收目标：

```bash
curl -sS -X PATCH http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/balloon \
  -H 'Content-Type: application/json' \
  -d '{"amountMiB":1024}'
```

`amountMiB` 是希望 balloon 从 guest 占用并归还给宿主机的目标内存量。设置为 `0` 表示让 balloon 完全 deflate，并不等同于修改 VM 启动时的总内存。

读取配置和统计：

```bash
curl -sS http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/balloon
curl -sS http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/balloon/statistics
```

调整统计轮询间隔：

```bash
curl -sS -X PATCH \
  http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/balloon/statistics \
  -H 'Content-Type: application/json' \
  -d '{"statsPollingIntervalS":2}'
```

free-page hinting 是一次性运行，不是周期任务：

```bash
curl -sS -X POST \
  http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/balloon/hinting/start \
  -H 'Content-Type: application/json' \
  -d '{"acknowledgeOnStop":true}'

curl -sS http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/balloon/hinting
curl -sS -X POST http://127.0.0.1:8080/v1/sandboxes/sbx-xxx/balloon/hinting/stop
```

状态字段：

- `state=stopped`：`hostCmd=0`。
- `state=completed`：`hostCmd=1`。
- `state=starting`：host 已发起命令，但 guest 尚未观察到同一命令。
- `state=running`：`hostCmd` 和 `guestCmd` 指向同一次运行。

Balloon 仅由 Firecracker driver 声明支持。gVisor、stub 和未实现的 runtime 会返回不支持错误。guest kernel 还必须启用 virtio-balloon，统计、回收和 page reporting 才会生效。

## Image

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/images \
  -H 'Content-Type: application/json' \
  -d '{"templateID":"tpl-xxx","imageID":"img-xxx","labels":{"env":"dev"}}'

curl -sS http://127.0.0.1:8080/v1/images
curl -sS http://127.0.0.1:8080/v1/images/img-xxx
curl -X DELETE http://127.0.0.1:8080/v1/images/img-xxx
```

转换 template：

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/templates/convert \
  -H 'Content-Type: application/json' \
  -d '{"templateID":"tpl-xxx","imageID":"img-xxx"}'
```

## Runtime 和能力

```bash
curl -sS http://127.0.0.1:8080/v1/runtimes
curl -sS http://127.0.0.1:8080/v1/runtimes/firecracker
curl -sS http://127.0.0.1:8080/v1/runtimes/firecracker/capabilities
curl -sS http://127.0.0.1:8080/v1/runtimes/gvisor/capabilities
```

能力字段包括启动来源、pause/resume、snapshot、GPU、网络、在线调整、balloon、优雅关机、串口和 jailer。调用某个高级操作前应先查询 capability，而不是仅根据 runtime 名称猜测。

## 当前路由汇总

```text
GET    /health
GET    /healthz

GET    /templates
GET    /templates/{template_id}
DELETE /templates/{template_id}
GET    /templates/{template_id}/builds/{build_id}/status

POST   /sandboxes
GET    /sandboxes
GET    /sandboxes/{sandbox_id}
DELETE /sandboxes/{sandbox_id}
POST   /sandboxes/{sandbox_id}/pause
POST   /sandboxes/{sandbox_id}/connect
POST   /sandboxes/{sandbox_id}/timeout

POST   /v1/sandboxes
GET    /v1/sandboxes
GET    /v1/sandboxes/{sandbox_id}
DELETE /v1/sandboxes/{sandbox_id}
POST   /v1/sandboxes/{sandbox_id}/pause
POST   /v1/sandboxes/{sandbox_id}/resume
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

GET    /v2/sandboxes
POST   /v2/templates/{template_id}/builds/{build_id}
GET    /v2/templates/{template_id}/builds/{build_id}/status
POST   /v3/templates
```

