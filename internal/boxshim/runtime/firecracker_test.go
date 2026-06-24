package runtime

import (
	"context"
	"os/exec"
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
