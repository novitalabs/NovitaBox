# Image CLI

Image 是 rootfs-only artifact：

```text
Firecracker Image = rootfs.ext4 + metadata
gVisor Image      = rootfs directory + metadata
```

从 template 创建：

```bash
boxctl image create tpl-xxxxxxxxxxxxxxxxxxxx
boxctl image create tpl-xxxxxxxxxxxxxxxxxxxx --image img-xxxxxxxxxxxxxxxxxxxx
boxctl image create tpl-xxxxxxxxxxxxxxxxxxxx --label env=dev --label owner=agent
```

查询和删除：

```bash
boxctl image list
boxctl image ls
boxctl image get img-xxxxxxxxxxxxxxxxxxxx
boxctl image delete img-xxxxxxxxxxxxxxxxxxxx
boxctl image rm img-xxxxxxxxxxxxxxxxxxxx
```

也可以通过 template 命令转换：

```bash
boxctl template convert tpl-xxxxxxxxxxxxxxxxxxxx --image img-xxxxxxxxxxxxxxxxxxxx
```

Image 不包含运行时内存状态，比 Firecracker template 更适合迁移和长期保存，但启动时需要重新创建 runtime 状态。

