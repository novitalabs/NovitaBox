# Sandbox CLI

Sandbox commands manage running MicroVM sandboxes.

```bash
boxctl sandbox [command]
```

## Create

Create from a template:

```bash
boxctl sandbox create tpl-xxxxxxxxxxxxxxxxxxxx
```

Equivalent form:

```bash
boxctl sandbox create --template tpl-xxxxxxxxxxxxxxxxxxxx
```

Create from an image:

```bash
boxctl sandbox create --image img-xxxxxxxxxxxxxxxxxxxx
```

Create from a snapshot:

```bash
boxctl sandbox create --snapshot snap-xxxxxxxxxxxxxxxxxxxx
```

Options:

- `--template <template_id>`: template id.
- `--image <image_id>`: image id.
- `--snapshot <snapshot_id>`: snapshot id.
- `--runtime <runtime_type>`: runtime type, usually `firecracker`.

## List and Get

```bash
boxctl sandbox list
boxctl sandbox ls
boxctl sandbox get sbx-xxxxxxxxxxxxxxxxxxxx
```

## Exec

Run a command:

```bash
boxctl sandbox exec sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh -c 'pwd && ls -alh'
```

Run with a working directory:

```bash
boxctl sandbox exec --cwd /root sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh -c 'pwd'
```

Open an interactive terminal:

```bash
boxctl sandbox exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
```

There is also a top-level alias:

```bash
boxctl exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
```

## Shell

```bash
boxctl sandbox shell sbx-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox shell --shell /bin/bash sbx-xxxxxxxxxxxxxxxxxxxx
```

If `/bin/bash` is not present in the rootfs, use `/bin/sh`.

## Pause and Resume

Pause creates sandbox-bound snapshot state and releases runtime resources when possible:

```bash
boxctl sandbox pause sbx-xxxxxxxxxxxxxxxxxxxx
```

Resume starts the runtime again from the sandbox snapshot:

```bash
boxctl sandbox resume sbx-xxxxxxxxxxxxxxxxxxxx
```

## Power Lifecycle

Gracefully power off:

```bash
boxctl sandbox poweroff sbx-xxxxxxxxxxxxxxxxxxxx
```

Power on from rootfs/snapshot files:

```bash
boxctl sandbox poweron sbx-xxxxxxxxxxxxxxxxxxxx
```

Reboot:

```bash
boxctl sandbox reboot sbx-xxxxxxxxxxxxxxxxxxxx
```

Hidden compatibility aliases are available:

```bash
boxctl sandbox stop sbx-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox start sbx-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox restart sbx-xxxxxxxxxxxxxxxxxxxx
```

## Delete

Terminate a sandbox and remove sandbox-bound snapshots:

```bash
boxctl sandbox delete sbx-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox rm sbx-xxxxxxxxxxxxxxxxxxxx
boxctl sandbox kill sbx-xxxxxxxxxxxxxxxxxxxx
```
