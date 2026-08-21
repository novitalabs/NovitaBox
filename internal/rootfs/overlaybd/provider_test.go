package overlaybd

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/novitalabs/NovitaBox/internal/config"
)

func TestNewContainerdProviderDoesNotRequireFeatureGate(t *testing.T) {
	socketPath := t.TempDir() + "/overlaybd.sock"
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("write placeholder socket: %v", err)
	}
	cfg := config.Default().OverlayBD
	cfg.SnapshotterSocket = socketPath

	provider, err := NewContainerdProvider(cfg)
	if err != nil {
		t.Fatalf("NewContainerdProvider() error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestProviderPrepareMountAndRemove(t *testing.T) {
	backend := &fakeBackend{
		resolved:      ResolvedImage{Reference: "registry.example/test@sha256:abc", Parent: "sha256:chain"},
		prepareMounts: []Mount{{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower"}}},
		remounts:      []Mount{{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower"}}},
	}
	mounter := &fakeMounter{}
	provider := NewProvider(backend, mounter)

	handle, err := provider.Prepare(context.Background(), PrepareRequest{
		SandboxID: "sbx-1",
		SourceRef: "registry.example/test:overlaybd",
		Target:    "/sandboxes/sbx-1/rootfs",
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if handle.SnapshotKey != "novitabox-sandbox-sbx-1" {
		t.Fatalf("snapshot key = %q", handle.SnapshotKey)
	}
	if handle.SourceDigest != "registry.example/test@sha256:abc" || handle.Parent != "sha256:chain" {
		t.Fatalf("handle = %#v", handle)
	}
	if !reflect.DeepEqual(mounter.mounted, []string{"/sandboxes/sbx-1/rootfs"}) {
		t.Fatalf("mounted targets = %#v", mounter.mounted)
	}

	if err := provider.Unmount(context.Background(), handle); err != nil {
		t.Fatalf("Unmount() error = %v", err)
	}
	if err := provider.Mount(context.Background(), handle); err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	if err := provider.Remove(context.Background(), handle); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if !reflect.DeepEqual(backend.removed, []string{handle.SnapshotKey}) {
		t.Fatalf("removed snapshots = %#v", backend.removed)
	}
	if got := len(mounter.unmounted); got != 2 {
		t.Fatalf("unmount count = %d, want 2", got)
	}
}

func TestProviderPrepareRollsBackSnapshotWhenMountFails(t *testing.T) {
	backend := &fakeBackend{
		resolved:      ResolvedImage{Reference: "image@sha256:abc", Parent: "sha256:chain"},
		prepareMounts: []Mount{{Type: "overlay"}},
	}
	mounter := &fakeMounter{mountErr: errors.New("mount failed")}
	provider := NewProvider(backend, mounter)

	_, err := provider.Prepare(context.Background(), PrepareRequest{
		SandboxID: "sbx-rollback",
		SourceRef: "image:tag",
		Target:    "/rootfs",
	})
	if err == nil {
		t.Fatal("Prepare() error = nil")
	}
	if !reflect.DeepEqual(backend.removed, []string{"novitabox-sandbox-sbx-rollback"}) {
		t.Fatalf("removed snapshots = %#v", backend.removed)
	}
}

type fakeBackend struct {
	resolved      ResolvedImage
	prepareMounts []Mount
	remounts      []Mount
	removed       []string
}

func (f *fakeBackend) EnsureImage(context.Context, string) (ResolvedImage, error) {
	return f.resolved, nil
}

func (f *fakeBackend) Prepare(context.Context, string, string, map[string]string) ([]Mount, error) {
	return f.prepareMounts, nil
}

func (f *fakeBackend) Mounts(context.Context, string) ([]Mount, error) {
	return f.remounts, nil
}

func (f *fakeBackend) Remove(_ context.Context, key string) error {
	f.removed = append(f.removed, key)
	return nil
}

func (f *fakeBackend) Close() error { return nil }

type fakeMounter struct {
	mounted   []string
	unmounted []string
	mountErr  error
}

func (f *fakeMounter) Mount(_ []Mount, target string) error {
	if f.mountErr != nil {
		return f.mountErr
	}
	f.mounted = append(f.mounted, target)
	return nil
}

func (f *fakeMounter) Unmount(target string) error {
	f.unmounted = append(f.unmounted, target)
	return nil
}
