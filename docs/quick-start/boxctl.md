# boxctl Quick Start

`boxctl` is the native NovitaBox command line client. It talks to:

- `boxapi` for lifecycle and artifact APIs, default `http://127.0.0.1:8080`
- `boxproxy` for process and terminal traffic, default `http://127.0.0.1:8082`

If NovitaBox is installed by `scripts/install-linux.sh`, the binary is installed at:

```bash
/data/novitabox/boxctl
```

## Health Check

```bash
curl http://127.0.0.1:8080/health
curl http://127.0.0.1:8082/healthz
```

## Build a Template

Build from a Docker image:

```bash
/data/novitabox/boxctl \
  --api http://127.0.0.1:8080 \
  template build my-template \
  --from-image ubuntu:22.04 \
  --run 'echo hello from novitabox'
```

Build steps:

- `--run` runs a shell command through `/bin/sh -c`; it can be repeated.
- `--exec` splits the value by whitespace and executes it directly; it can be repeated.
- `--start-cmd` and `--ready-cmd` are passed as template lifecycle commands.
- `--template` can be used to choose a template id; otherwise one is generated.

Example:

```bash
/data/novitabox/boxctl template build python-template \
  --from-image ubuntu:22.04 \
  --run 'apt-get update' \
  --run 'apt-get install -y python3 python3-pip' \
  --exec 'python3 --version'
```

The response includes `templateID` and `buildID`. Save the `templateID` for sandbox creation.

## List and Inspect Templates

```bash
/data/novitabox/boxctl template list
/data/novitabox/boxctl template get tpl-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl template status tpl-xxxxxxxxxxxxxxxxxxxx <build_id>
```

Delete a template:

```bash
/data/novitabox/boxctl template delete tpl-xxxxxxxxxxxxxxxxxxxx
```

## Create a Sandbox

```bash
/data/novitabox/boxctl \
  --api http://127.0.0.1:8080 \
  sandbox create tpl-xxxxxxxxxxxxxxxxxxxx
```

The response includes `sandboxID`.

You can also create from an image:

```bash
/data/novitabox/boxctl sandbox create --image img-xxxxxxxxxxxxxxxxxxxx
```

## Execute Commands

Run a one-shot command:

```bash
/data/novitabox/boxctl \
  --proxy http://127.0.0.1:8082 \
  exec sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh -c 'pwd && ls -alh'
```

Open an interactive shell:

```bash
/data/novitabox/boxctl \
  --proxy http://127.0.0.1:8082 \
  exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
```

The sandbox subcommand exposes the same exec flow:

```bash
/data/novitabox/boxctl sandbox exec -it sbx-xxxxxxxxxxxxxxxxxxxx /bin/sh
/data/novitabox/boxctl sandbox shell sbx-xxxxxxxxxxxxxxxxxxxx
```

## Sandbox Lifecycle

```bash
/data/novitabox/boxctl sandbox list
/data/novitabox/boxctl sandbox get sbx-xxxxxxxxxxxxxxxxxxxx

/data/novitabox/boxctl sandbox pause sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox resume sbx-xxxxxxxxxxxxxxxxxxxx

/data/novitabox/boxctl sandbox poweroff sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox poweron sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox reboot sbx-xxxxxxxxxxxxxxxxxxxx

/data/novitabox/boxctl sandbox delete sbx-xxxxxxxxxxxxxxxxxxxx
```

`delete` also has aliases:

```bash
/data/novitabox/boxctl sandbox rm sbx-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl sandbox kill sbx-xxxxxxxxxxxxxxxxxxxx
```

## Images

Create a rootfs-only image from a template:

```bash
/data/novitabox/boxctl image create tpl-xxxxxxxxxxxxxxxxxxxx \
  --image img-xxxxxxxxxxxxxxxxxxxx \
  --label env=dev
```

Manage images:

```bash
/data/novitabox/boxctl image list
/data/novitabox/boxctl image get img-xxxxxxxxxxxxxxxxxxxx
/data/novitabox/boxctl image delete img-xxxxxxxxxxxxxxxxxxxx
```

Convert a template to an image:

```bash
/data/novitabox/boxctl template convert tpl-xxxxxxxxxxxxxxxxxxxx \
  --image img-xxxxxxxxxxxxxxxxxxxx
```

## Runtime Capabilities

```bash
/data/novitabox/boxctl runtime list
/data/novitabox/boxctl runtime show firecracker
/data/novitabox/boxctl runtime capabilities firecracker
```

## Troubleshooting

Check service logs:

```bash
journalctl -u novitabox-boxapi -n 200 --no-pager
journalctl -u novitabox-boxlet -n 200 --no-pager
journalctl -u novitabox-boxproxy -n 200 --no-pager
```

If template build reaches `starting boxd` and then fails with:

```text
Failed to get KVM vcpu msr: 0x3a
```

the guest booted and the agent started, but Firecracker full snapshot creation is not supported by the current host KVM environment. This commonly happens on some nested KVM setups. Use a host with working Firecracker snapshot support or build from a rootfs/image flow that does not require full VM snapshots.
