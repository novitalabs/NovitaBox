---
title: 未发布变更
---

# 未发布变更

本页记录当前工作区已经实现、但尚未归入正式版本号的能力。

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

