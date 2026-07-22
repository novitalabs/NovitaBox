# Image CLI

Images are rootfs-only artifacts. They are more portable than templates because they do not include runtime memory state. Firecracker images use `rootfs.ext4`; gVisor images use a directory rootfs.

```bash
boxctl image [command]
```

## Create

Create an image from a template:

```bash
boxctl image create tpl-xxxxxxxxxxxxxxxxxxxx
```

Choose an image id:

```bash
boxctl image create tpl-xxxxxxxxxxxxxxxxxxxx \
  --image img-xxxxxxxxxxxxxxxxxxxx
```

Add labels:

```bash
boxctl image create tpl-xxxxxxxxxxxxxxxxxxxx \
  --label env=dev \
  --label owner=agent
```

## List

```bash
boxctl image list
boxctl image ls
```

## Get

```bash
boxctl image get img-xxxxxxxxxxxxxxxxxxxx
```

## Delete

```bash
boxctl image delete img-xxxxxxxxxxxxxxxxxxxx
boxctl image rm img-xxxxxxxxxxxxxxxxxxxx
```

## Template Conversion

The template command also exposes conversion:

```bash
boxctl template convert tpl-xxxxxxxxxxxxxxxxxxxx \
  --image img-xxxxxxxxxxxxxxxxxxxx
```

Runtime notes:

- Converting a Firecracker template drops `memfile` and `snapfile`.
- Converting a gVisor template copies the directory rootfs.
