package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
