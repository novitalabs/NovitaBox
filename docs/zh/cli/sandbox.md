# Sandbox CLI

## 创建

从 template：

```bash
boxctl sandbox create tpl-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox create --template tpl-xxxxxxxxxxxxxxxxxxxx
```

从 image 或 snapshot：

```bash
boxctl sandbox create --image img-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox create --snapshot snap-xxxxxxxxxxxxxxxxxxxx
```

指定 runtime 和 GPU：

```bash
boxctl sandbox create \
  --template tpl-xxxxxxxxxxxxxxxxxxxx \
  --runtime gvisor \
  --gpu 1
```

GPU 当前只在 gVisor driver 中实现，依赖宿主机 NVIDIA driver、CDI spec 和 `runsc --nvproxy`。

直接从已经转换好的 OverlayBD image 创建 gVisor sandbox，不需要 template build：

```bash
boxctl sandbox create \
  --overlaybd-image registry.example.com/team/ubuntu:overlaybd
```

节点需要部署 containerd、OverlayBD snapshotter 和 OverlayBD TCMU 服务。sandbox rootfs 请求已经显式选择 OverlayBD，因此 boxlet 不需要额外的启用开关。

## 查询和删除

```bash
boxctl sandbox list
boxctl sandbox ls
boxctl sandbox get sbx-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox delete sbx-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox rm sbx-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox kill sbx-xxxxxxxxxxxxxxxxxxxx
```

## Exec 和 Shell

```bash
boxctl sandbox exec sbx-xxx /bin/sh -c 'pwd && ls -alh'
boxctl sandbox exec --cwd /root sbx-xxx /bin/sh -c pwd
boxctl sandbox exec -it sbx-xxx /bin/sh
boxctl exec -it sbx-xxx /bin/sh
boxctl sandbox shell sbx-xxx
boxctl sandbox shell --shell /bin/bash --cwd /root sbx-xxx
```

rootfs 不一定提供 `/bin/bash`，通用脚本优先使用 `/bin/sh`。

## 生命周期

```bash
boxctl sandbox pause sbx-xxx
boxctl sandbox resume sbx-xxx
boxctl sandbox poweroff sbx-xxx
boxctl sandbox poweron sbx-xxx
boxctl sandbox reboot sbx-xxx
```

`stop`、`start`、`restart` 是隐藏兼容命令，分别映射到 poweroff、poweron 和 reboot。pause/resume 依赖 runtime capability。

## Firecracker Balloon

设置和读取目标：

```bash
boxctl sandbox balloon set sbx-xxx --amount-mib 1024
boxctl sandbox balloon get sbx-xxx
boxctl sandbox balloon set sbx-xxx --amount-mib 0
```

统计：

```bash
boxctl sandbox balloon stats sbx-xxx
boxctl sandbox balloon stats-interval sbx-xxx --interval-s 2
```

free-page hinting：

```bash
boxctl sandbox balloon hinting start sbx-xxx
boxctl sandbox balloon hinting get sbx-xxx
boxctl sandbox balloon hinting stop sbx-xxx
```

`hinting start` 默认发送 `acknowledgeOnStop=true`；需要调用方自行控制完成确认时，可以使用：

```bash
boxctl sandbox balloon hinting start sbx-xxx --acknowledge-on-stop=false
```

Balloon 只支持 Firecracker。它要求 guest kernel 启用 virtio-balloon；API 成功只代表 Firecracker 接受配置，不保证 guest 内一定有可回收页。
