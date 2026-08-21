package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/novitalabs/NovitaBox/internal/sandbox"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"github.com/novitalabs/NovitaBox/internal/storage/store/sqlite"
)

func TestSandboxLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	defer st.Close()

	err := st.CreateSandbox(ctx, store.SandboxRecord{
		ID:          "sbx-1",
		State:       sandbox.StateCreating,
		RuntimeType: "firecracker",
		TemplateID:  "tpl-1",
	})
	if err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}

	record, err := st.GetSandbox(ctx, "sbx-1")
	if err != nil {
		t.Fatalf("GetSandbox() error = %v", err)
	}
	if record.State != sandbox.StateCreating {
		t.Fatalf("sandbox state = %q, want %q", record.State, sandbox.StateCreating)
	}
	if record.RuntimeType != "firecracker" {
		t.Fatalf("runtime type = %q, want firecracker", record.RuntimeType)
	}

	if err := st.UpdateSandboxState(ctx, "sbx-1", sandbox.StateCreating, sandbox.StateRunning, "create"); err != nil {
		t.Fatalf("UpdateSandboxState() error = %v", err)
	}

	record, err = st.GetSandbox(ctx, "sbx-1")
	if err != nil {
		t.Fatalf("GetSandbox() after update error = %v", err)
	}
	if record.State != sandbox.StateRunning {
		t.Fatalf("sandbox state = %q, want %q", record.State, sandbox.StateRunning)
	}

	transitions, err := st.ListTransitions(ctx, "sandbox", "sbx-1")
	if err != nil {
		t.Fatalf("ListTransitions() error = %v", err)
	}
	if len(transitions) != 1 {
		t.Fatalf("transition count = %d, want 1", len(transitions))
	}
	if transitions[0].FromState != string(sandbox.StateCreating) || transitions[0].ToState != string(sandbox.StateRunning) {
		t.Fatalf("transition = %q -> %q, want creating -> running", transitions[0].FromState, transitions[0].ToState)
	}
}

func TestSandboxStateUpdateRequiresExpectedState(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	defer st.Close()

	if err := st.CreateSandbox(ctx, store.SandboxRecord{ID: "sbx-1", State: sandbox.StateRunning}); err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}

	err := st.UpdateSandboxState(ctx, "sbx-1", sandbox.StatePaused, sandbox.StateRunning, "resume")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateSandboxState() error = %v, want ErrNotFound", err)
	}

	record, err := st.GetSandbox(ctx, "sbx-1")
	if err != nil {
		t.Fatalf("GetSandbox() error = %v", err)
	}
	if record.State != sandbox.StateRunning {
		t.Fatalf("sandbox state = %q, want %q", record.State, sandbox.StateRunning)
	}
}

func TestSandboxNetworkSlotLease(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	defer st.Close()

	if err := st.CreateSandbox(ctx, store.SandboxRecord{ID: "sbx-1", State: sandbox.StateRunning}); err != nil {
		t.Fatalf("CreateSandbox(sbx-1) error = %v", err)
	}
	if err := st.CreateSandbox(ctx, store.SandboxRecord{ID: "sbx-2", State: sandbox.StateRunning}); err != nil {
		t.Fatalf("CreateSandbox(sbx-2) error = %v", err)
	}

	slot1, err := st.AssignSandboxNetworkSlot(ctx, "sbx-1", 2)
	if err != nil {
		t.Fatalf("AssignSandboxNetworkSlot(sbx-1) error = %v", err)
	}
	if slot1 != 1 {
		t.Fatalf("slot1 = %d, want 1", slot1)
	}
	slot2, err := st.AssignSandboxNetworkSlot(ctx, "sbx-2", 2)
	if err != nil {
		t.Fatalf("AssignSandboxNetworkSlot(sbx-2) error = %v", err)
	}
	if slot2 != 2 {
		t.Fatalf("slot2 = %d, want 2", slot2)
	}

	if err := st.ReleaseSandboxNetworkSlot(ctx, "sbx-1"); err != nil {
		t.Fatalf("ReleaseSandboxNetworkSlot() error = %v", err)
	}
	slot1Again, err := st.AssignSandboxNetworkSlot(ctx, "sbx-1", 2)
	if err != nil {
		t.Fatalf("AssignSandboxNetworkSlot(sbx-1 again) error = %v", err)
	}
	if slot1Again != 1 {
		t.Fatalf("slot1Again = %d, want released slot 1", slot1Again)
	}
}

func TestArtifactCRUD(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	defer st.Close()

	if err := st.CreateImage(ctx, store.ImageRecord{ID: "img-1", RootfsPath: "/rootfs.img"}); err != nil {
		t.Fatalf("CreateImage() error = %v", err)
	}
	if err := st.CreateTemplate(ctx, store.TemplateRecord{
		ID:           "tpl-1",
		RootfsPath:   "/rootfs.ext4",
		MemfilePath:  "/memfile",
		SnapfilePath: "/snapfile",
	}); err != nil {
		t.Fatalf("CreateTemplate() error = %v", err)
	}
	if err := st.CreateSnapshot(ctx, store.SnapshotRecord{
		ID:           "snap-1",
		SandboxID:    "sbx-1",
		RootfsPath:   "/snap/rootfs.ext4",
		MemfilePath:  "/snap/memfile",
		SnapfilePath: "/snap/snapfile",
	}); err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}

	images, err := st.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages() error = %v", err)
	}
	if len(images) != 1 || images[0].ID != "img-1" {
		t.Fatalf("images = %#v, want img-1", images)
	}

	template, err := st.GetTemplate(ctx, "tpl-1")
	if err != nil {
		t.Fatalf("GetTemplate() error = %v", err)
	}
	if template.SnapfilePath != "/snapfile" {
		t.Fatalf("template snapfile = %q, want /snapfile", template.SnapfilePath)
	}

	snapshots, err := st.ListSnapshotsBySandbox(ctx, "sbx-1")
	if err != nil {
		t.Fatalf("ListSnapshotsBySandbox() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != "snap-1" {
		t.Fatalf("snapshots = %#v, want snap-1", snapshots)
	}

	if err := st.DeleteImage(ctx, "img-1"); err != nil {
		t.Fatalf("DeleteImage() error = %v", err)
	}
	_, err = st.GetImage(ctx, "img-1")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetImage() error = %v, want ErrNotFound", err)
	}
}

func TestOverlayBDRootfsMetadataPersistence(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	defer st.Close()

	if err := st.CreateSandbox(ctx, store.SandboxRecord{
		ID:                 "sbx-overlaybd",
		State:              sandbox.StateCreating,
		RuntimeType:        "gvisor",
		RootfsProvider:     "overlaybd",
		RootfsSourceRef:    "registry.example/team/image:tag",
		RootfsSourceDigest: "",
		RootfsSnapshotKey:  "novitabox-sandbox-sbx-overlaybd",
	}); err != nil {
		t.Fatalf("CreateSandbox() error = %v", err)
	}
	sandboxRecord, err := st.GetSandbox(ctx, "sbx-overlaybd")
	if err != nil {
		t.Fatalf("GetSandbox() error = %v", err)
	}
	if sandboxRecord.RootfsProvider != "overlaybd" || sandboxRecord.RootfsSnapshotKey == "" {
		t.Fatalf("sandbox rootfs metadata = %#v", sandboxRecord)
	}
	if err := st.UpdateSandboxRootfsDigest(ctx, sandboxRecord.ID, "sha256:resolved"); err != nil {
		t.Fatalf("UpdateSandboxRootfsDigest() error = %v", err)
	}
	sandboxRecord, err = st.GetSandbox(ctx, sandboxRecord.ID)
	if err != nil || sandboxRecord.RootfsSourceRef != "registry.example/team/image:tag" || sandboxRecord.RootfsSourceDigest != "sha256:resolved" {
		t.Fatalf("updated sandbox rootfs source = %#v, %v", sandboxRecord, err)
	}
}

func TestDeleteTemplateRemovesBuilds(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	defer st.Close()

	if err := st.CreateTemplate(ctx, store.TemplateRecord{
		ID:           "tpl-1",
		RootfsPath:   "/rootfs.ext4",
		MemfilePath:  "/memfile",
		SnapfilePath: "/snapfile",
	}); err != nil {
		t.Fatalf("CreateTemplate() error = %v", err)
	}
	if err := st.CreateTemplateBuild(ctx, store.TemplateBuildRecord{
		ID:         "build-1",
		TemplateID: "tpl-1",
		Status:     store.TemplateBuildStatusWaiting,
	}); err != nil {
		t.Fatalf("CreateTemplateBuild() error = %v", err)
	}

	if err := st.DeleteTemplate(ctx, "tpl-1"); err != nil {
		t.Fatalf("DeleteTemplate() error = %v", err)
	}
	_, err := st.GetTemplate(ctx, "tpl-1")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTemplate() error = %v, want ErrNotFound", err)
	}
	_, err = st.GetTemplateBuild(ctx, "tpl-1", "build-1")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTemplateBuild() error = %v, want ErrNotFound", err)
	}
}

func TestTemplateBuildLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	defer st.Close()

	if err := st.CreateTemplateBuild(ctx, store.TemplateBuildRecord{
		ID:         "build-1",
		TemplateID: "tpl-1",
		Status:     store.TemplateBuildStatusWaiting,
	}); err != nil {
		t.Fatalf("CreateTemplateBuild() error = %v", err)
	}

	build, err := st.GetTemplateBuild(ctx, "tpl-1", "build-1")
	if err != nil {
		t.Fatalf("GetTemplateBuild() error = %v", err)
	}
	if build.Status != store.TemplateBuildStatusWaiting {
		t.Fatalf("build status = %q, want %q", build.Status, store.TemplateBuildStatusWaiting)
	}

	if err := st.UpdateTemplateBuildStatus(ctx, "tpl-1", "build-1", store.TemplateBuildStatusWaiting, store.TemplateBuildStatusBuilding); err != nil {
		t.Fatalf("UpdateTemplateBuildStatus() error = %v", err)
	}

	build, err = st.GetTemplateBuild(ctx, "tpl-1", "build-1")
	if err != nil {
		t.Fatalf("GetTemplateBuild() after update error = %v", err)
	}
	if build.Status != store.TemplateBuildStatusBuilding {
		t.Fatalf("build status = %q, want %q", build.Status, store.TemplateBuildStatusBuilding)
	}

	if err := st.CreateTemplateBuild(ctx, store.TemplateBuildRecord{
		ID:         "build-2",
		TemplateID: "tpl-1",
		Status:     store.TemplateBuildStatusReady,
	}); err != nil {
		t.Fatalf("CreateTemplateBuild(build-2) error = %v", err)
	}
	builds, err := st.ListTemplateBuilds(ctx, "tpl-1")
	if err != nil {
		t.Fatalf("ListTemplateBuilds() error = %v", err)
	}
	if len(builds) != 2 {
		t.Fatalf("len(builds) = %d, want 2", len(builds))
	}
	if builds[0].ID != "build-2" || builds[1].ID != "build-1" {
		t.Fatalf("build order = %#v, want build-2 then build-1", builds)
	}
}

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "novitabox.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	return st
}
