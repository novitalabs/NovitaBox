# Sandbox 生命周期

状态大致如下：

```text
Creating -> Running
Running  -> Pausing  -> Paused
Paused   -> Resuming -> Running
Running  -> Stopping -> Stopped
Stopped  -> Starting -> Running
Running  -> Rebooting -> Running
*        -> Killing  -> Killed
失败时   -> Failed / Unknown
```

## Create

boxlet 准备 artifact、网络和 sandbox 目录，启动 boxshim，再由 driver 创建 runtime。创建来源可以是 template、image 或 snapshot，但是否支持取决于 runtime capability。

## Pause/Resume

Firecracker pause 会保存 sandbox-bound full snapshot，并停止当前 VM；resume 从该 snapshot 恢复。gVisor 当前返回不支持。

## Poweroff/Poweron

poweroff 停止 runtime，但保留 sandbox rootfs、metadata 和可恢复文件。poweron 使用保留状态重新启动。它与 delete/kill 不同。

## Reboot

由 boxshim 停止并重新启动同一 runtime spec。运行时错误会写入 `RuntimeInfo.error_message`，Firecracker 错误通常还会附带日志尾部。

## Delete/Kill

终止 runtime、关闭 shim、清理网络、mount 和 sandbox 文件，并更新/删除 metadata。异常退出后如果清理未完成，应检查：

- shim/runtime 进程
- jailer mount
- network namespace
- host veth 和 route
- sandbox 目录

## Balloon 生命周期

Balloon 是 Firecracker running 状态下的在线操作，不创建新的 sandbox 状态：

- target 可随时增减。
- statistics polling interval 可在线修改。
- free-page hinting 是一次性 run，需要显式 start/stop。
- runtime 退出或 API socket 不可用时，balloon 请求会失败。

