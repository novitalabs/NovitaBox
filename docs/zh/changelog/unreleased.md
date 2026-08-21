---
title: 未发布变更
---

# 未发布变更

本页记录当前工作区已经实现、但尚未归入正式版本号的能力。

## gVisor OverlayBD rootfs

- gVisor sandbox 可以直接从已经转换好的 OverlayBD image 启动，不经过 template build。
- 新增基于 containerd snapshot 和 lease API 的 OverlayBD rootfs provider。
- 使用 OverlayBD 扩展版 `ctr rpull` 准备 lazy-pull image。
- 为每个 sandbox 分别持久化原始 image 引用、解析后的 digest、rootfs provider 和 active snapshot key。
- sandbox get/list API 直接返回 OverlayBD image 和 snapshot 元数据。
- `GET /v1/sandboxes` 改为与 template list 一致的顶层数组结构，空列表返回 `[]`。
- boxshim 清理 runsc/CDI 子挂载时保留 OverlayBD rootfs 主挂载。
- poweroff 时卸载、poweron 时重新挂载、kill 时通过 snapshotter API 删除 active snapshot。
- 增加 boxlet OverlayBD 配置参数，以及 `boxctl sandbox create --overlaybd-image`。

## Firecracker Balloon

- 在 `RuntimeSpec` 中增加 `BalloonSpec`。
- Firecracker 创建时配置 virtio-balloon，默认 target 为 `0 MiB`。
- 默认启用 `deflate_on_oom`、statistics、free-page hinting 和 free-page reporting。
- 支持在线设置 balloon target。
- 支持读取完整 balloon statistics。
- 支持在线调整 statistics polling interval。
- 支持启动、停止和查询一次性 free-page hinting run。
- 为 Firecracker 创建和配置 `firecracker-metrics.json`。
- 通过 boxshim、boxlet、boxapi 和 boxctl 暴露完整调用链。
- gVisor、stub 和 unsupported driver 返回明确的不支持错误。
- 增加 Firecracker API、默认配置和 hinting 状态单元测试。

## 已知差异

- Firecracker boxshim capability 已设置 `balloon=true`。
- boxlet 静态 Firecracker capability 尚未同步 balloon 字段，public runtime capability API 可能仍显示 false。
- 当前 `boxctl template build` 没有 `--runtime` 参数；gVisor template 需要通过 v3 API 指定 `runtimeType`。
