package config

import "testing"

func TestDefaultOverlayBDConfig(t *testing.T) {
	cfg := Default().OverlayBD
	if cfg.ContainerdAddress != "/run/containerd/containerd.sock" {
		t.Fatalf("containerd address = %q", cfg.ContainerdAddress)
	}
	if cfg.Namespace != "novitabox" || cfg.Snapshotter != "overlaybd" {
		t.Fatalf("overlaybd config = %#v", cfg)
	}
	if cfg.CtrBinaryPath != "/opt/overlaybd/snapshotter/ctr" {
		t.Fatalf("ctr path = %q", cfg.CtrBinaryPath)
	}
}
