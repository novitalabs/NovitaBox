## Internal RPC

### Boxlet Sandbox Service

```proto
service BoxletSandboxService {
  rpc CreateSandbox(CreateSandboxRequest) returns (SandboxInfo);
  rpc PauseSandbox(PauseSandboxRequest) returns (SnapshotInfo);
  rpc ResumeSandbox(ResumeSandboxRequest) returns (SandboxInfo);
  rpc KillSandbox(KillSandboxRequest) returns (google.protobuf.Empty);

  rpc StartSandbox(StartSandboxRequest) returns (SandboxInfo);
  rpc StopSandbox(StopSandboxRequest) returns (SandboxInfo);
  rpc RebootSandbox(RebootSandboxRequest) returns (SandboxInfo);

  rpc GetSandbox(GetSandboxRequest) returns (SandboxInfo);
  rpc ListSandboxes(ListSandboxesRequest) returns (ListSandboxesResponse);
}
```

`CreateSandboxRequest.runtime_type` selects the runtime backend. `RuntimeSpec.machine.gpu` requests the number of NVIDIA GPUs for runtimes that support GPU. Today GPU is implemented for gVisor.

### Boxlet Artifact Service

```proto
service BoxletArtifactService {
  rpc CreateTemplate(CreateTemplateRequest) returns (TemplateInfo);
  rpc DeleteTemplate(DeleteTemplateRequest) returns (google.protobuf.Empty);
  rpc GetTemplate(GetTemplateRequest) returns (TemplateInfo);
  rpc ListTemplates(ListTemplatesRequest) returns (ListTemplatesResponse);

  rpc CreateImage(CreateImageRequest) returns (ImageInfo);
  rpc DeleteImage(DeleteImageRequest) returns (google.protobuf.Empty);
  rpc GetImage(GetImageRequest) returns (ImageInfo);
  rpc ListImages(ListImagesRequest) returns (ListImagesResponse);
}
```

Templates store runtime metadata. Firecracker templates use `rootfs.ext4`, `memfile`, and `snapfile`; gVisor templates use a directory rootfs and no VM snapshot files.

### Boxlet Node Service

```proto
service BoxletNodeService {
  rpc NodeStatus(NodeStatusRequest) returns (NodeStatus);
  rpc ListRuntimes(ListRuntimesRequest) returns (ListRuntimesResponse);
  rpc GetRuntimeCapabilities(GetRuntimeCapabilitiesRequest) returns (RuntimeCapabilities);
}
```

`ListRuntimes` and `GetRuntimeCapabilities` report Firecracker, gVisor, and Cloud Hypervisor capabilities. Clients should check capabilities before assuming pause/resume, snapshot, GPU, or networking behavior.

### BoxShim Service

```proto
service BoxShim {
  rpc CreateRuntime(CreateRuntimeRequest) returns (RuntimeInfo);
  rpc PauseRuntime(PauseRuntimeRequest) returns (RuntimeInfo);
  rpc ResumeRuntime(ResumeVMRequest) returns (RuntimeInfo);
  rpc KillRuntime(KillRuntimeRequest) returns (google.protobuf.Empty);

  rpc StartRuntime(StartRuntimeRequest) returns (RuntimeInfo);
  rpc StopRuntime(StopRuntimeRequest) returns (RuntimeInfo);
  rpc RebootRuntime(RebootRuntimeRequest) returns (RuntimeInfo);

  rpc Status(StatusRequest) returns (RuntimeInfo);
  rpc Capabilities(CapabilitiesRequest) returns (RuntimeCapabilities);
}
```
