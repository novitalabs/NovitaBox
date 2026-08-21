# RPC API

NovitaBox 内部使用 gRPC 将控制面、节点 agent 和每 sandbox runtime supervisor 分开。

```text
boxapi
  -> BoxletSandboxService / BoxletArtifactService / BoxletRuntimeService
boxlet
  -> 每个 sandbox 的 BoxShim
boxshim
  -> Firecracker API、runsc 或其他 runtime driver
```

## BoxletSandboxService

负责 sandbox 生命周期：

- `CreateSandbox`
- `PauseSandbox`
- `ResumeSandbox`
- `KillSandbox`
- `StartSandbox`
- `StopSandbox`
- `RebootSandbox`
- `GetSandbox`
- `ListSandboxes`

Balloon RPC：

- `UpdateSandboxBalloon`
- `GetSandboxBalloon`
- `GetSandboxBalloonStats`
- `UpdateSandboxBalloonStats`
- `StartSandboxBalloonHinting`
- `StopSandboxBalloonHinting`
- `GetSandboxBalloonHinting`

boxlet 在转发 balloon 请求前会读取 sandbox runtime capability。只有 `balloon=true` 时才连接 boxshim 执行操作。

## BoxletArtifactService

负责 template、image 和 artifact：

- 创建、查询、列出和删除 template。
- 从 Docker image、template 或现有 artifact 物化 rootfs。
- 构建 Firecracker snapshot template 或 gVisor directory-rootfs template。
- template 转 image。
- 创建、查询、列出和删除 image。

## BoxletRuntimeService

用于查询 runtime 信息和 capability。上层 API 应使用 capability 判断操作是否支持，例如：

- `pause`
- `resume`
- `full_snapshot`
- `gpu`
- `tap_network`
- `balloon`
- `jailer`

## BoxShim

每个 sandbox 对应一个 shim socket。BoxShim RPC 包括：

- `CreateRuntime`
- `StartRuntime`
- `ResumeRuntime`
- `PauseRuntime`
- `StopRuntime`
- `KillRuntime`
- `RebootRuntime`
- `Status`
- `Capabilities`
- balloon 配置、统计和 hinting RPC

Driver 接口要求所有 runtime 实现同一组方法。暂不支持某能力的 runtime 应返回明确错误，而不是静默忽略。

## 核心消息

`RuntimeSpec` 描述 runtime 启动所需信息：

- sandbox ID 和 runtime type
- CPU、内存、hugepages 和 GPU 数量
- kernel、rootfs、snapshot、网络、agent 和 jailer
- extra drives
- balloon 初始配置
- labels 和 annotations

`RuntimeCapabilities` 是运行时能力协商契约。gVisor 在 protobuf 中使用 `RUNTIME_TYPE_CONTAINER`，HTTP 层将其规范化为 `gvisor`。

`BalloonStats` 包括 target/actual MiB，以及 swap、page fault、free/available memory、cache、HugeTLB、OOM、scan 和 reclaim 等 guest 统计字段。字段是否有有效值取决于 guest kernel 是否上报。

## 生成代码

修改以下 proto 后，需要同步生成 Go 文件：

```text
proto/novitabox/v1/types.proto
proto/novitabox/v1/boxlet.proto
proto/novitabox/v1/boxshim.proto
```

生成结果位于：

```text
internal/pb/novitabox/v1/*.pb.go
internal/pb/novitabox/v1/*_grpc.pb.go
```

不要只修改生成文件；消息和 RPC 的源定义应始终以 `.proto` 为准。

