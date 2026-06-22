create
create a sandbox from a template or a snapshot, or image + start_cmd

pause
create a sandbox-bound snapshot and release runtime resources when possible

resume
create runtime again and resume from sandbox-bound snapshot

kill
terminate sandbox lifecycle and remove sandbox-bound snapshots

list
list all sandboxes

exec
exec a command in a sandbox

shell
launch a shell

commit
create a template from sandbox, need to pause sandbox

* poweroff(stop)
  safety shutdown, save all state to rootfs

* poweron(start)
  start a sandbox from rootfs

* reboot(restart)
  gracefully shut down and restart

clone
clone a new sandbox from given sandbox
