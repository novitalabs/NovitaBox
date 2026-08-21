# boxctl CLI

`boxctl` 是 NovitaBox 的本地管理 CLI。

```bash
boxctl --api http://127.0.0.1:8080 \
       --proxy http://127.0.0.1:8082 \
       <command>
```

- `--api`：template、image、sandbox 生命周期、runtime 和 balloon。
- `--proxy`：exec、交互式 shell 和 sandbox 数据面连接。

## 命令组

- [Template](template.md)：创建、构建、查询、转换和删除。
- [Sandbox](sandbox.md)：创建、执行命令、生命周期、GPU 和 balloon。
- [Image](image.md)：从 template 创建 rootfs-only image。
- [Runtime](runtime.md)：查询 runtime 和 capability。

别名：

```text
sandbox -> sbx
template -> tpl
image -> img
list -> ls
delete -> rm
```

常用示例：

```bash
boxctl template build python-dev --from-image ubuntu:22.04 --run 'apt-get update'
boxctl sandbox create tpl-xxxxxxxxxxxxxxxxxxxx
boxctl exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
boxctl sandbox balloon set sbx-xxxxxxxxxxxxxxxxxxxx --amount-mib 512
boxctl runtime capabilities firecracker
```

