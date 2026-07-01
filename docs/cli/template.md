# Template CLI

Template commands manage fast-start sandbox templates.

```bash
boxctl template [command]
```

## Create

Create a template record and build id without running the build:

```bash
boxctl template create my-template
```

Options:

- `--template <template_id>`: choose a template id.
- `--cpu <count>`: template CPU count.
- `--memory <mb>`: template memory in MB.

## Build

Create and build a template in one command:

```bash
boxctl template build my-template \
  --from-image ubuntu:22.04 \
  --run 'echo hello'
```

Sources:

- `--from-image <image>`: build from a Docker image, for example `ubuntu:22.04`.
- `--from-template <template_id>`: build from an existing template.

Build commands:

- `--run <command>`: run a shell command through `/bin/sh -c`.
- `--exec <command>`: split the command by whitespace and execute it directly.
- `--start-cmd <command>`: template start command.
- `--ready-cmd <command>`: template ready command.

Other options:

- `--template <template_id>`: choose a template id.
- `--cpu <count>`: template CPU count.
- `--memory <mb>`: template memory in MB.

Examples:

```bash
boxctl template build python-template \
  --from-image ubuntu:22.04 \
  --run 'apt-get update' \
  --run 'apt-get install -y python3' \
  --exec 'python3 --version'
```

## List

```bash
boxctl template list
```

Alias:

```bash
boxctl template ls
```

## Get

```bash
boxctl template get tpl-xxxxxxxxxxxxxxxxxxxx
```

## Build Status

```bash
boxctl template status tpl-xxxxxxxxxxxxxxxxxxxx <build_id>
```

## Delete

```bash
boxctl template delete tpl-xxxxxxxxxxxxxxxxxxxx
boxctl template rm tpl-xxxxxxxxxxxxxxxxxxxx
```

## Convert to Image

Convert a template to a rootfs-only image:

```bash
boxctl template convert tpl-xxxxxxxxxxxxxxxxxxxx \
  --image img-xxxxxxxxxxxxxxxxxxxx
```

The image drops `memfile` and `snapfile`; it keeps only rootfs state.
