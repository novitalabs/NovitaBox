# Sandbox CLI

Sandbox commands manage running sandboxes across supported runtimes.

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
- `--runtime <runtime_type>`: runtime type, for example `firecracker` or `gvisor`.
- `--gpu <count>`: number of NVIDIA GPUs to expose to the sandbox. GPU support is currently implemented for gVisor.

Create a gVisor sandbox:

```bash
boxctl sandbox create \
  --template tpl-xxxxxxxxxxxxxxxxxxxx \
  --runtime gvisor
```

Create a gVisor sandbox with one NVIDIA GPU:

```bash
boxctl sandbox create \
  --template tpl-xxxxxxxxxxxxxxxxxxxx \
  --runtime gvisor \
  --gpu 1
```

Validate GPU access:

```bash
boxctl sandbox exec sbx-xxxxxxxxxxxxxxxxxxxx /usr/bin/nvidia-smi
boxctl sandbox exec sbx-xxxxxxxxxxxxxxxxxxxx /cuda-samples/vectorAdd
```

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

Pause/resume snapshots are runtime dependent. Firecracker supports VM snapshot pause/resume. gVisor pause/resume snapshots are not supported yet.

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
