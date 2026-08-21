package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"github.com/novitalabs/NovitaBox/internal/storage/store/sqlite"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestCreateSandboxForwardsGPUCount(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	root := t.TempDir()
	cfg := config.Default()
	cfg.RootDir = root
	cfg.Storage.DBPath = filepath.Join(root, "db", "novitabox.db")

	st, err := sqlite.Open(context.Background(), cfg.Storage.DBPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})

	client := &fakeSandboxClient{}
	h := New(cfg, log.NewNop(), st, client, nil)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader(`{"templateID":"tpl-test","runtime_type":"gvisor","gpu":2}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.CreateSandbox(c)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if client.createReq == nil {
		t.Fatal("expected CreateSandbox to be called")
	}
	if got := client.createReq.GetRuntimeSpec().GetMachine().GetGpu(); got != 2 {
		t.Fatalf("runtimeSpec.machine.gpu = %d, want 2", got)
	}
}

func TestCreateSandboxForwardsOverlayBDRootfsSource(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)

	root := t.TempDir()
	cfg := config.Default()
	cfg.RootDir = root
	cfg.Storage.DBPath = filepath.Join(root, "db", "novitabox.db")

	st, err := sqlite.Open(context.Background(), cfg.Storage.DBPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	client := &fakeSandboxClient{}
	h := New(cfg, log.NewNop(), st, client, nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", strings.NewReader(`{
		"runtime_type":"gvisor",
		"rootfs":{"provider":"overlaybd","image":"registry.example/team/image:tag","pullMode":"lazy"}
	}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	h.CreateSandbox(c)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	source := client.createReq.GetRootfsSource()
	if source.GetProvider() != "overlaybd" || source.GetImage() != "registry.example/team/image:tag" || source.GetPullMode() != "lazy" {
		t.Fatalf("rootfs source = %#v", source)
	}
}

func TestSandboxInfoResponseIncludesOverlayBDRootfsMetadata(t *testing.T) {
	got := sandboxRecordInfoResponse(store.SandboxRecord{
		ID:                 "sbx-obd",
		RootfsProvider:     "overlaybd",
		RootfsSourceRef:    "registry.example/team/image:tag",
		RootfsSourceDigest: "sha256:resolved",
		RootfsSnapshotKey:  "novitabox-sandbox-sbx-obd",
	})

	if got.Rootfs == nil || got.Rootfs.Provider != "overlaybd" || got.Rootfs.Image != "registry.example/team/image:tag" || got.Rootfs.Digest != "sha256:resolved" || got.Rootfs.SnapshotKey != "novitabox-sandbox-sbx-obd" {
		t.Fatalf("rootfs response = %#v", got.Rootfs)
	}
}

func TestSandboxProtoInfoResponseIncludesOverlayBDRootfsMetadata(t *testing.T) {
	got := sandboxProtoInfoResponse(&novitaboxv1.SandboxInfo{
		SandboxId: "sbx-obd",
		Rootfs: &novitaboxv1.RootfsInfo{
			Provider:    "overlaybd",
			Image:       "registry.example/team/image:tag",
			Digest:      "sha256:resolved",
			SnapshotKey: "novitabox-sandbox-sbx-obd",
		},
	})

	if got.Rootfs == nil || got.Rootfs.Provider != "overlaybd" || got.Rootfs.Image != "registry.example/team/image:tag" || got.Rootfs.Digest != "sha256:resolved" || got.Rootfs.SnapshotKey != "novitabox-sandbox-sbx-obd" {
		t.Fatalf("rootfs response = %#v", got.Rootfs)
	}
}

type fakeSandboxClient struct {
	createReq *novitaboxv1.CreateSandboxRequest
}

func (f *fakeSandboxClient) CreateSandbox(_ context.Context, in *novitaboxv1.CreateSandboxRequest, _ ...grpc.CallOption) (*novitaboxv1.SandboxInfo, error) {
	f.createReq = in
	return &novitaboxv1.SandboxInfo{
		SandboxId:   in.GetSandboxId(),
		TemplateId:  in.GetTemplateId(),
		RuntimeType: in.GetRuntimeType(),
	}, nil
}

func (f *fakeSandboxClient) PauseSandbox(context.Context, *novitaboxv1.PauseSandboxRequest, ...grpc.CallOption) (*novitaboxv1.SnapshotInfo, error) {
	return nil, nil
}

func (f *fakeSandboxClient) ResumeSandbox(context.Context, *novitaboxv1.ResumeSandboxRequest, ...grpc.CallOption) (*novitaboxv1.SandboxInfo, error) {
	return nil, nil
}

func (f *fakeSandboxClient) KillSandbox(context.Context, *novitaboxv1.KillSandboxRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (f *fakeSandboxClient) StartSandbox(context.Context, *novitaboxv1.StartSandboxRequest, ...grpc.CallOption) (*novitaboxv1.SandboxInfo, error) {
	return nil, nil
}

func (f *fakeSandboxClient) StopSandbox(context.Context, *novitaboxv1.StopSandboxRequest, ...grpc.CallOption) (*novitaboxv1.SandboxInfo, error) {
	return nil, nil
}

func (f *fakeSandboxClient) RebootSandbox(context.Context, *novitaboxv1.RebootSandboxRequest, ...grpc.CallOption) (*novitaboxv1.SandboxInfo, error) {
	return nil, nil
}

func (f *fakeSandboxClient) GetSandbox(context.Context, *novitaboxv1.GetSandboxRequest, ...grpc.CallOption) (*novitaboxv1.SandboxInfo, error) {
	return nil, nil
}

func (f *fakeSandboxClient) ListSandboxes(context.Context, *novitaboxv1.ListSandboxesRequest, ...grpc.CallOption) (*novitaboxv1.ListSandboxesResponse, error) {
	return nil, nil
}

func (f *fakeSandboxClient) UpdateSandboxBalloon(context.Context, *novitaboxv1.UpdateSandboxBalloonRequest, ...grpc.CallOption) (*novitaboxv1.BalloonConfig, error) {
	return nil, nil
}

func (f *fakeSandboxClient) GetSandboxBalloon(context.Context, *novitaboxv1.GetSandboxBalloonRequest, ...grpc.CallOption) (*novitaboxv1.BalloonConfig, error) {
	return nil, nil
}

func (f *fakeSandboxClient) GetSandboxBalloonStats(context.Context, *novitaboxv1.GetSandboxBalloonStatsRequest, ...grpc.CallOption) (*novitaboxv1.BalloonStats, error) {
	return nil, nil
}

func (f *fakeSandboxClient) UpdateSandboxBalloonStats(context.Context, *novitaboxv1.UpdateSandboxBalloonStatsRequest, ...grpc.CallOption) (*novitaboxv1.BalloonConfig, error) {
	return nil, nil
}

func (f *fakeSandboxClient) StartSandboxBalloonHinting(context.Context, *novitaboxv1.StartSandboxBalloonHintingRequest, ...grpc.CallOption) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, nil
}

func (f *fakeSandboxClient) StopSandboxBalloonHinting(context.Context, *novitaboxv1.StopSandboxBalloonHintingRequest, ...grpc.CallOption) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, nil
}

func (f *fakeSandboxClient) GetSandboxBalloonHinting(context.Context, *novitaboxv1.GetSandboxBalloonHintingRequest, ...grpc.CallOption) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, nil
}
