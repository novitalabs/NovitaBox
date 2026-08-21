# Template CLI

Template 保存可快速启动的 runtime artifact。

## 创建记录

```bash
boxctl template create python-dev
boxctl template create python-dev --template tpl-xxxxxxxxxxxxxxxxxxxx --cpu 2 --memory 2048
```

命令会调用 `POST /v3/templates` 并返回 template ID 和 build ID，但不会自动完成 build。

## 创建并构建

```bash
boxctl template build python-dev \
  --from-image ubuntu:22.04 \
  --run 'apt-get update' \
  --run 'apt-get install -y python3 python3-pip' \
  --exec 'python3 --version'
```

参数：

- `--template`：指定 template ID。
- `--cpu`：build runtime 的 CPU 数。
- `--memory`：build runtime 的内存 MiB。
- `--from-image`：Docker/OCI image。
- `--from-template`：已有 template ID。
- `--start-cmd`：template 启动命令。
- `--ready-cmd`：ready 检查命令。
- `--run`：shell build step，可重复。
- `--exec`：按空格拆成参数的直接执行 step，可重复。

当前代码中的 `boxctl template create/build` 没有 `--runtime` 参数，因此 CLI 创建的是 API 默认 runtime，也就是 Firecracker。需要创建 gVisor template 时，请使用 [HTTP API](../api/http.md) 中的 gVisor template 流程设置 `runtimeType: "gvisor"`。

## 查询状态

```bash
boxctl template status tpl-xxxxxxxxxxxxxxxxxxxx build-id
boxctl template list
boxctl template get tpl-xxxxxxxxxxxxxxxxxxxx
```

## 转换为 Image

```bash
boxctl template convert tpl-xxxxxxxxxxxxxxxxxxxx --image img-xxxxxxxxxxxxxxxxxxxx
```

- Firecracker 转换会保留 rootfs，丢弃 `memfile` 和 `snapfile`。
- gVisor 转换会复制 directory rootfs。

## 删除

```bash
boxctl template delete tpl-xxxxxxxxxxxxxxxxxxxx
boxctl template rm tpl-xxxxxxxxxxxxxxxxxxxx
```
