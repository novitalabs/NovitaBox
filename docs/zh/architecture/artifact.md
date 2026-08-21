# Artifact 模型

## Image

只包含 rootfs 状态：

```text
Firecracker Image = rootfs.ext4 + metadata
gVisor Image      = rootfs directory + metadata
```

Image 不携带运行时内存，更适合迁移、复制和长期存储。

## Template

保存选定 runtime 的快速启动状态：

```text
Firecracker Template = rootfs.ext4 + memfile + snapfile + metadata
gVisor Template      = rootfs directory + metadata
```

Firecracker template 通过 full snapshot 快速恢复 VM；gVisor template 本质是准备完成的 OCI directory rootfs。

## Snapshot

Snapshot 是 pause 产生的 sandbox-bound 状态：

```text
Firecracker Snapshot = sandbox rootfs.ext4 + memfile + snapfile + metadata
```

它属于 sandbox 生命周期，不是面向用户的通用转换 artifact。gVisor 当前不支持这种 pause/resume snapshot。

## 转换关系

```text
Docker/OCI image -> Template
Template -> Sandbox
Template -> Image
Image -> Sandbox
Firecracker Sandbox --pause--> Snapshot --resume--> Sandbox
```

Firecracker template 转 image 会丢弃内存和 snapshot 文件。gVisor template 转 image 会复制 directory rootfs。

## CoW 和文件系统

NovitaBox 优先使用 reflink 复制大文件和目录，以减少 template、image、sandbox 之间的复制成本。安装脚本推荐 Btrfs；如果底层文件系统不支持 reflink，相关路径可能退化为普通复制，空间和耗时都会增加。

