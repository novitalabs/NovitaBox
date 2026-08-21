package runtime

import "testing"

func TestPathPreservedMatchesOnlyExactMount(t *testing.T) {
	preserved := []string{"/var/lib/novitabox/sandboxes/sbx/rootfs"}
	if !pathPreserved("/var/lib/novitabox/sandboxes/sbx/rootfs", preserved) {
		t.Fatal("rootfs mount should be preserved")
	}
	if pathPreserved("/var/lib/novitabox/sandboxes/sbx/rootfs/proc", preserved) {
		t.Fatal("nested runtime mount should not be preserved")
	}
}
