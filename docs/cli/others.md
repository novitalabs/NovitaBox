
## runtime

list
list all runtime

show
show runtime capabilities


## snapshot

show
show sandbox snapshot info
# Runtime CLI

Runtime commands show what the local runtime backend can do.

```bash
boxctl runtime [command]
```

## List

```bash
boxctl runtime list
```

## Show

```bash
boxctl runtime show firecracker
```

## Capabilities

```bash
boxctl runtime capabilities firecracker
```

Capability fields describe whether the runtime supports:

- start from image
- start from template
- start from snapshot
- pause and resume
- full snapshots
- networking

## Global Flags

All `boxctl` commands support:

```bash
--api http://127.0.0.1:8080
--proxy http://127.0.0.1:8082
```

Use `--api` for lifecycle and artifact APIs. Use `--proxy` for process, shell, and sandbox traffic.
