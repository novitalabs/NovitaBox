package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	"github.com/novitalabs/NovitaBox/internal/rootfs/overlaybd"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
)

func TestPrepareSandboxRuntimeFilesUsesOverlayBDWithoutTemplate(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.RootDir = root
	cfg.Template.BoxdBinaryPath = filepath.Join(root, "boxd")
	if err := os.WriteFile(cfg.Template.BoxdBinaryPath, []byte("boxd"), 0o755); err != nil {
		t.Fatalf("write boxd: %v", err)
	}
	provider := &fakeOverlayBDProvider{}
	svc := newSandboxService(cfg, log.NewNop(), nil)
	svc.overlayBD = provider
	record := store.SandboxRecord{
		ID:                "sbx-obd",
		RuntimeType:       "gvisor",
		RootfsProvider:    overlaybd.ProviderName,
		RootfsSourceRef:   "registry.example/team/image:tag",
		RootfsSnapshotKey: overlaybd.SnapshotKey("sbx-obd"),
	}
	spec := svc.runtimeSpecForSandbox(record)

	if err := svc.prepareSandboxRuntimeFiles(context.Background(), record, spec); err != nil {
		t.Fatalf("prepareSandboxRuntimeFiles() error = %v", err)
	}
	if provider.prepare.SourceRef != record.RootfsSourceRef || provider.prepare.SandboxID != record.ID {
		t.Fatalf("prepare request = %#v", provider.prepare)
	}
	guestBoxd := filepath.Join(spec.GetRootfs().GetPath(), "novitabox", "agent", "boxd")
	if data, err := os.ReadFile(guestBoxd); err != nil || string(data) != "boxd" {
		t.Fatalf("injected boxd = %q, %v", data, err)
	}
}

func TestOverlayBDLifecycleHelpersUsePersistedSnapshotKey(t *testing.T) {
	cfg := config.Default()
	cfg.RootDir = t.TempDir()
	provider := &fakeOverlayBDProvider{}
	svc := newSandboxService(cfg, log.NewNop(), nil)
	svc.overlayBD = provider
	record := store.SandboxRecord{
		ID:                "sbx-obd",
		RootfsProvider:    overlaybd.ProviderName,
		RootfsSourceRef:   "image@sha256:abc",
		RootfsSnapshotKey: "persisted-key",
	}

	if err := svc.mountOverlayBDRootfs(context.Background(), record); err != nil {
		t.Fatalf("mountOverlayBDRootfs() error = %v", err)
	}
	if err := svc.unmountOverlayBDRootfs(context.Background(), record); err != nil {
		t.Fatalf("unmountOverlayBDRootfs() error = %v", err)
	}
	if err := svc.removeOverlayBDRootfs(context.Background(), record); err != nil {
		t.Fatalf("removeOverlayBDRootfs() error = %v", err)
	}
	if provider.mounted.SnapshotKey != "persisted-key" || provider.unmounted.SnapshotKey != "persisted-key" || provider.removed.SnapshotKey != "persisted-key" {
		t.Fatalf("handles = mount:%#v unmount:%#v remove:%#v", provider.mounted, provider.unmounted, provider.removed)
	}
}

func TestSandboxRecordToProtoIncludesOverlayBDRootfsMetadata(t *testing.T) {
	record := store.SandboxRecord{
		ID:                 "sbx-obd",
		RootfsProvider:     overlaybd.ProviderName,
		RootfsSourceRef:    "registry.example/team/image:tag",
		RootfsSourceDigest: "sha256:resolved",
		RootfsSnapshotKey:  "novitabox-sandbox-sbx-obd",
	}

	info := sandboxRecordToProto(record, 0)
	rootfs := info.GetRootfs()
	if rootfs.GetProvider() != overlaybd.ProviderName || rootfs.GetImage() != record.RootfsSourceRef || rootfs.GetDigest() != record.RootfsSourceDigest || rootfs.GetSnapshotKey() != record.RootfsSnapshotKey {
		t.Fatalf("rootfs info = %#v", rootfs)
	}
}

type fakeOverlayBDProvider struct {
	prepare   overlaybd.PrepareRequest
	mounted   overlaybd.Handle
	unmounted overlaybd.Handle
	removed   overlaybd.Handle
}

func (f *fakeOverlayBDProvider) Prepare(_ context.Context, req overlaybd.PrepareRequest) (overlaybd.Handle, error) {
	f.prepare = req
	if err := os.MkdirAll(req.Target, 0o755); err != nil {
		return overlaybd.Handle{}, err
	}
	return overlaybd.Handle{SnapshotKey: overlaybd.SnapshotKey(req.SandboxID), SourceRef: req.SourceRef, Target: req.Target}, nil
}

func (f *fakeOverlayBDProvider) Mount(_ context.Context, handle overlaybd.Handle) error {
	f.mounted = handle
	return nil
}

func (f *fakeOverlayBDProvider) Unmount(_ context.Context, handle overlaybd.Handle) error {
	f.unmounted = handle
	return nil
}

func (f *fakeOverlayBDProvider) Remove(_ context.Context, handle overlaybd.Handle) error {
	f.removed = handle
	return nil
}

func (f *fakeOverlayBDProvider) Close() error { return nil }
