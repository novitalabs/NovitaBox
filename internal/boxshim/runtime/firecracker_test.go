package runtime

import (
	"strings"
	"testing"

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
