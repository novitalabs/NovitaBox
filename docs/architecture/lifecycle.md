## Sandbox Lifecycle

Supported sandbox operations:

- create
- pause, when supported by the selected runtime
- resume, when supported by the selected runtime
- kill
- list
- exec
- shell

Extended maintenance operations:

- poweroff
- poweron
- reboot

Firecracker balloon operations are online maintenance operations while the runtime is running:

- update the balloon target without restarting the VM
- read balloon statistics
- change the statistics polling interval
- start, inspect, and stop one-shot free-page hinting

Balloon operations do not create a new sandbox lifecycle state. They fail if the Firecracker API socket or runtime process is unavailable, and they are not supported by gVisor.

Lifecycle state machine:

```text
create:
  Template -> Creating -> Running

pause:
  Running -> Pausing -> Paused

resume:
  Paused -> Resuming -> Running

stop/poweroff:
  Running -> Stopping -> Stopped

start/poweron:
  Stopped -> Starting -> Running

reboot:
  Running -> Rebooting -> Running

kill:
  any state -> Killing -> Killed

failure:
  any active transition -> Failed

unknown:
  state cannot be confirmed during recovery -> Unknown
```

Runtime notes:

- Firecracker supports VM snapshot-oriented template startup and pause/resume.
- gVisor supports create, stop, reboot, kill, exec, shell, and delete. It does not currently support pause/resume snapshots.
- Deleting a sandbox unmounts leaked runtime mounts under the sandbox directory before removing rootfs files. This is especially important for gVisor GPU sandboxes because NVIDIA CDI hooks can temporarily mount `/proc` and `/run/nvidia-ctk-hook`.
