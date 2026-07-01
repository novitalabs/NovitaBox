## Sandbox Lifecycle

Supported sandbox operations:

- create
- pause
- resume
- kill
- list
- exec
- shell

Extended maintenance operations:

- poweroff
- poweron
- reboot

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
