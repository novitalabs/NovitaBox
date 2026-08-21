# Runtime CLI

```bash
boxctl runtime list
boxctl runtime show firecracker
boxctl runtime show gvisor
boxctl runtime capabilities firecracker
boxctl runtime capabilities gvisor
```

常见 capability：

- `startFromImage`
- `startFromTemplate`
- `startFromSnapshot`
- `pause` / `resume`
- `fullSnapshot` / `diffSnapshot`
- `gpu`
- `vsock`
- `tapNetwork`
- `hotplugDisk` / `hotplugNetwork`
- `liveResizeCPU` / `liveResizeMemory`
- `balloon`
- `gracefulShutdown`
- `serialConsole`
- `jailer`

目前 Firecracker 声明支持 balloon。gVisor 声明 GPU 能力，但不支持 Firecracker 风格的 pause/resume snapshot 和 balloon。

