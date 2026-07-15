package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
)

func TestBootArgsIncludeInitPath(t *testing.T) {
	got := bootArgs(&novitaboxv1.RuntimeSpec{
		Kernel: &novitaboxv1.KernelSpec{
			InitPath: "/novitabox/init",
		},
	})

	if !strings.Contains(got, "init=/novitabox/init") {
		t.Fatalf("boot args = %q, want init=/novitabox/init", got)
	}
	if !strings.Contains(got, "root=/dev/vda") {
		t.Fatalf("boot args = %q, want root=/dev/vda", got)
	}
	if !strings.Contains(got, "8250.nr_uarts=0") {
		t.Fatalf("boot args = %q, want 8250.nr_uarts=0", got)
	}
}

func TestBootArgsDoNotOverrideInitArg(t *testing.T) {
	got := bootArgs(&novitaboxv1.RuntimeSpec{
		Kernel: &novitaboxv1.KernelSpec{
			InitPath:   "/novitabox/init",
			KernelArgs: []string{"console=ttyS0", "init=/custom/init"},
		},
	})

	if strings.Contains(got, "init=/novitabox/init") {
		t.Fatalf("boot args = %q, should not append init path when init arg exists", got)
	}
	if !strings.Contains(got, "init=/custom/init") {
		t.Fatalf("boot args = %q, want existing init arg", got)
	}
}

func TestBootArgsIncludeNetworkIPArg(t *testing.T) {
	got := bootArgs(&novitaboxv1.RuntimeSpec{
		Kernel: &novitaboxv1.KernelSpec{},
		Network: &novitaboxv1.NetworkSpec{
			GuestIp:   "169.254.0.21",
			GatewayIp: "169.254.0.22",
		},
	})

	want := "ip=169.254.0.21::169.254.0.22:255.255.255.252::eth0:off"
	if !strings.Contains(got, want) {
		t.Fatalf("boot args = %q, want %s", got, want)
	}
}

func TestBootArgsDoNotOverrideNetworkIPArg(t *testing.T) {
	got := bootArgs(&novitaboxv1.RuntimeSpec{
		Kernel: &novitaboxv1.KernelSpec{
			KernelArgs: []string{"root=/dev/vda", "ip=10.0.0.2::10.0.0.1:255.255.255.0::eth0:off"},
		},
		Network: &novitaboxv1.NetworkSpec{
			GuestIp:   "169.254.0.21",
			GatewayIp: "169.254.0.22",
		},
	})

	if strings.Contains(got, "ip=169.254.0.21::169.254.0.22") {
		t.Fatalf("boot args = %q, should not append generated ip arg when ip arg exists", got)
	}
	if !strings.Contains(got, "ip=10.0.0.2::10.0.0.1:255.255.255.0::eth0:off") {
		t.Fatalf("boot args = %q, want existing ip arg", got)
	}
}

func TestWaitPostStartAliveDetectsExitedProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	driver := &FirecrackerDriver{cmd: cmd, waitCh: waitCh}
	err := driver.waitPostStartAliveLocked(context.Background(), time.Second)
	if err == nil {
		t.Fatal("waitPostStartAliveLocked() error = nil, want exited process error")
	}
	if !strings.Contains(err.Error(), "firecracker exited shortly after start") {
		t.Fatalf("error = %q, want firecracker exited shortly after start", err.Error())
	}
}

func TestFirecrackerCommandUsesNetworkNamespace(t *testing.T) {
	cmd := firecrackerCommand("/opt/firecracker", "/run/fc.sock", &novitaboxv1.NetworkSpec{NamespaceName: "nb-test"})
	if cmd.Path != "ip" {
		t.Fatalf("cmd path = %q, want ip", cmd.Path)
	}
	want := []string{"ip", "netns", "exec", "nb-test", "/opt/firecracker", "--api-sock", "/run/fc.sock"}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("cmd args = %#v, want %#v", cmd.Args, want)
	}
}

func TestFirecrackerCommandWithoutNetworkNamespace(t *testing.T) {
	cmd := firecrackerCommand("/opt/firecracker", "/run/fc.sock", nil)
	if cmd.Path != "/opt/firecracker" {
		t.Fatalf("cmd path = %q, want /opt/firecracker", cmd.Path)
	}
	want := []string{"/opt/firecracker", "--api-sock", "/run/fc.sock"}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("cmd args = %#v, want %#v", cmd.Args, want)
	}
}

func TestFirecrackerJailerCommand(t *testing.T) {
	cmd := firecrackerJailerCommand("/opt/firecracker", &firecrackerJailerSpec{
		binaryPath:    "/opt/jailer",
		chrootDir:     "/srv/jailer",
		uid:           "1000",
		gid:           "1001",
		newPIDNS:      true,
		cgroupVersion: "2",
		parentCgroup:  "novitabox",
		cgroups:       []string{"memory.max=805306368", "cpu.max=100000 100000", "pids.max=512"},
		resourceLimit: []string{"no-file=4096"},
	}, "sbx-test", "/fc.sock")
	if cmd.Path != "/opt/jailer" {
		t.Fatalf("cmd path = %q, want /opt/jailer", cmd.Path)
	}
	want := []string{"/opt/jailer", "--id", "sbx-test", "--exec-file", "/opt/firecracker", "--uid", "1000", "--gid", "1001", "--chroot-base-dir", "/srv/jailer", "--new-pid-ns", "--cgroup-version", "2", "--parent-cgroup", "novitabox", "--cgroup", "memory.max=805306368", "--cgroup", "cpu.max=100000 100000", "--cgroup", "pids.max=512", "--resource-limit", "no-file=4096", "--", "--api-sock", "/fc.sock"}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("cmd args = %#v, want %#v", cmd.Args, want)
	}
}

func TestFirecrackerJailerCommandUsesNetworkNamespace(t *testing.T) {
	cmd := firecrackerJailerNetworkCommand("/opt/firecracker", &firecrackerJailerSpec{
		binaryPath: "/opt/jailer",
		chrootDir:  "/srv/jailer",
		uid:        "1000",
		gid:        "1001",
	}, "sbx-test", "/fc.sock", &novitaboxv1.NetworkSpec{NamespaceName: "nb-test"})
	if cmd.Path != "/opt/jailer" {
		t.Fatalf("cmd path = %q, want /opt/jailer", cmd.Path)
	}
	want := []string{"/opt/jailer", "--id", "sbx-test", "--exec-file", "/opt/firecracker", "--uid", "1000", "--gid", "1001", "--chroot-base-dir", "/srv/jailer", "--netns", "/var/run/netns/nb-test", "--", "--api-sock", "/fc.sock"}
	if strings.Join(cmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("cmd args = %#v, want %#v", cmd.Args, want)
	}
}

func TestPrepareFirecrackerLaunchUsesShortJailerAPISocketPath(t *testing.T) {
	root := t.TempDir()
	driver := &FirecrackerDriver{cfg: configWithRoot(root)}
	spec := &novitaboxv1.RuntimeSpec{
		SandboxId: "template-build-tpl-e929b5lgglj6oilryjhw",
	}
	sandboxDir := filepath.Join(root, "sandboxes", spec.GetSandboxId())
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		t.Fatalf("create sandbox dir: %v", err)
	}

	launch, err := driver.prepareFirecrackerLaunch(spec, sandboxDir)
	if err != nil {
		t.Fatalf("prepareFirecrackerLaunch() error = %v", err)
	}
	want := jailerHostAPISocketPath(spec.GetSandboxId(), filepath.Join(root, "sandboxes", spec.GetSandboxId(), "jails", "firecracker", spec.GetSandboxId(), "root", "fc.sock"))
	if launch.apiSocket != want {
		t.Fatalf("apiSocket = %q, want %q", launch.apiSocket, want)
	}
	if len(launch.apiSocket) >= 108 {
		t.Fatalf("apiSocket length = %d, want below unix socket path limit", len(launch.apiSocket))
	}
	target, err := os.Readlink(launch.apiSocket)
	if err != nil {
		t.Fatalf("read api socket link: %v", err)
	}
	if !strings.HasSuffix(target, "/jails/firecracker/"+spec.GetSandboxId()+"/root/fc.sock") {
		t.Fatalf("api socket link target = %q, want jailer root fc.sock", target)
	}
}

func TestEffectiveJailerSpecUsesDefaults(t *testing.T) {
	driver := &FirecrackerDriver{
		cfg: configWithRoot("/srv/novitabox"),
	}
	spec := &novitaboxv1.RuntimeSpec{SandboxId: "sbx-test"}
	jailer, useJailer, err := driver.effectiveJailerSpec(spec)
	if err != nil {
		t.Fatalf("effectiveJailerSpec() error = %v", err)
	}
	if !useJailer {
		t.Fatal("effectiveJailerSpec() useJailer = false, want true")
	}
	if jailer.binaryPath != "/srv/novitabox/jailer" {
		t.Fatalf("jailer binaryPath = %q, want /srv/novitabox/jailer", jailer.binaryPath)
	}
	if jailer.chrootDir != "/srv/novitabox/sandboxes/sbx-test/jails" {
		t.Fatalf("jailer chrootDir = %q, want /srv/novitabox/sandboxes/sbx-test/jails", jailer.chrootDir)
	}
	if jailer.uid != jailerDefaultUID || jailer.gid == "" {
		t.Fatalf("jailer uid/gid = %q/%q, want uid %s and non-empty gid", jailer.uid, jailer.gid, jailerDefaultUID)
	}
	if !jailer.newPIDNS {
		t.Fatal("jailer newPIDNS = false, want true")
	}
	if len(jailer.resourceLimit) == 0 {
		t.Fatal("jailer resourceLimit is empty, want no-file limit")
	}
}

func TestEffectiveJailerSpecAllowsNetwork(t *testing.T) {
	driver := &FirecrackerDriver{
		cfg: configWithRoot("/srv/novitabox"),
	}
	spec := &novitaboxv1.RuntimeSpec{
		SandboxId: "sbx-test",
		Network:   &novitaboxv1.NetworkSpec{NamespaceName: "nb-test", TapName: "tap0"},
	}
	_, useJailer, err := driver.effectiveJailerSpec(spec)
	if err != nil {
		t.Fatalf("effectiveJailerSpec() error = %v", err)
	}
	if !useJailer {
		t.Fatal("effectiveJailerSpec() useJailer = false, want true")
	}
}

func TestJailerGuestDrivePathUsesAgentDirectory(t *testing.T) {
	got := jailerGuestDrivePath(&novitaboxv1.DriveSpec{
		DriveId: "agent",
		Path:    "/srv/novitabox/agents/boxd-test.ext4",
	})
	if got != "/agent/boxd.ext4" {
		t.Fatalf("jailerGuestDrivePath(agent) = %q, want /agent/boxd.ext4", got)
	}

	got = jailerGuestDrivePath(&novitaboxv1.DriveSpec{
		DriveId: "data",
		Path:    "/srv/novitabox/data.raw",
	})
	if got != "/extra-drives/data.raw" {
		t.Fatalf("jailerGuestDrivePath(data) = %q, want /extra-drives/data.raw", got)
	}
}

func TestJailerRuntimePathMapping(t *testing.T) {
	j := &firecrackerJailerRuntime{}
	if err := j.bindForTest("/data/sbx/snapshot", "/snapshot", true); err != nil {
		t.Fatalf("bindForTest(snapshot): %v", err)
	}
	if err := j.bindForTest("/data/sbx/rootfs.ext4", "/rootfs.ext4", false); err != nil {
		t.Fatalf("bindForTest(rootfs): %v", err)
	}

	if got := j.runtimePath("/data/sbx/snapshot/memfile.tmp"); got != "/snapshot/memfile.tmp" {
		t.Fatalf("runtimePath(snapshot tmp) = %q, want /snapshot/memfile.tmp", got)
	}
	if got := j.runtimePath("/data/sbx/rootfs.ext4"); got != "/rootfs.ext4" {
		t.Fatalf("runtimePath(rootfs) = %q, want /rootfs.ext4", got)
	}
}

func TestAtomicSnapshotPathsCommitReplacesFiles(t *testing.T) {
	root := t.TempDir()
	memfile := filepath.Join(root, "memfile")
	snapfile := filepath.Join(root, "snapfile")
	if err := os.WriteFile(memfile, []byte("old-mem"), 0o644); err != nil {
		t.Fatalf("write old memfile: %v", err)
	}
	if err := os.WriteFile(snapfile, []byte("old-snap"), 0o644); err != nil {
		t.Fatalf("write old snapfile: %v", err)
	}

	paths, err := prepareAtomicSnapshotPaths(memfile, snapfile)
	if err != nil {
		t.Fatalf("prepareAtomicSnapshotPaths() error = %v", err)
	}
	defer paths.cleanup()
	if err := os.WriteFile(paths.tmpMemfilePath, []byte("new-mem"), 0o644); err != nil {
		t.Fatalf("write tmp memfile: %v", err)
	}
	if err := os.WriteFile(paths.tmpSnapfilePath, []byte("new-snap"), 0o644); err != nil {
		t.Fatalf("write tmp snapfile: %v", err)
	}

	if err := paths.commit(); err != nil {
		t.Fatalf("commit() error = %v", err)
	}
	assertFileContent(t, memfile, "new-mem")
	assertFileContent(t, snapfile, "new-snap")
	if _, err := os.Stat(paths.oldMemfilePath); !os.IsNotExist(err) {
		t.Fatalf("old memfile stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(paths.oldSnapfilePath); !os.IsNotExist(err) {
		t.Fatalf("old snapfile stat error = %v, want not exist", err)
	}
}

func TestAtomicSnapshotPathsCommitKeepsOldFilesWhenTemporaryFileMissing(t *testing.T) {
	root := t.TempDir()
	memfile := filepath.Join(root, "memfile")
	snapfile := filepath.Join(root, "snapfile")
	if err := os.WriteFile(memfile, []byte("old-mem"), 0o644); err != nil {
		t.Fatalf("write old memfile: %v", err)
	}
	if err := os.WriteFile(snapfile, []byte("old-snap"), 0o644); err != nil {
		t.Fatalf("write old snapfile: %v", err)
	}

	paths, err := prepareAtomicSnapshotPaths(memfile, snapfile)
	if err != nil {
		t.Fatalf("prepareAtomicSnapshotPaths() error = %v", err)
	}
	defer paths.cleanup()
	if err := os.WriteFile(paths.tmpMemfilePath, []byte("new-mem"), 0o644); err != nil {
		t.Fatalf("write tmp memfile: %v", err)
	}

	if err := paths.commit(); err == nil {
		t.Fatal("commit() error = nil, want missing temporary snapfile error")
	}
	assertFileContent(t, memfile, "old-mem")
	assertFileContent(t, snapfile, "old-snap")
}

func configWithRoot(root string) config.Config {
	cfg := config.Default()
	cfg.RootDir = root
	return cfg
}

func (j *firecrackerJailerRuntime) bindForTest(hostPath string, guestPath string, isDir bool) error {
	j.pathMap = append(j.pathMap, jailerPathMapping{
		hostPath:  filepath.Clean(hostPath),
		guestPath: cleanGuestPath(guestPath),
		isDir:     isDir,
	})
	return nil
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s content = %q, want %q", path, string(data), want)
	}
}
