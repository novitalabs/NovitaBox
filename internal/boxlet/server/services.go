package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	shimruntime "github.com/novitalabs/NovitaBox/internal/boxshim/runtime"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/rootfs/overlaybd"
	"github.com/novitalabs/NovitaBox/internal/sandbox"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type sandboxService struct {
	novitaboxv1.UnimplementedBoxletSandboxServiceServer
	cfg       config.Config
	logger    *log.Logger
	store     store.Store
	overlayBD overlayBDRootfsProvider
}

type overlayBDRootfsProvider interface {
	Prepare(context.Context, overlaybd.PrepareRequest) (overlaybd.Handle, error)
	Mount(context.Context, overlaybd.Handle) error
	Unmount(context.Context, overlaybd.Handle) error
	Remove(context.Context, overlaybd.Handle) error
	Close() error
}

func newSandboxService(cfg config.Config, logger *log.Logger, store store.Store) *sandboxService {
	return &sandboxService{cfg: cfg, logger: logger, store: store}
}

func (s *sandboxService) overlayBDProvider() (overlayBDRootfsProvider, func(), error) {
	if s.overlayBD != nil {
		return s.overlayBD, func() {}, nil
	}
	provider, err := overlaybd.NewContainerdProvider(s.cfg.OverlayBD)
	if err != nil {
		return nil, nil, err
	}
	return provider, func() { _ = provider.Close() }, nil
}

func (s *sandboxService) CreateSandbox(ctx context.Context, req *novitaboxv1.CreateSandboxRequest) (*novitaboxv1.SandboxInfo, error) {
	sandboxID := req.GetSandboxId()
	if sandboxID == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id is required")
	}

	runtimeType := req.GetRuntimeType()
	if runtimeType == novitaboxv1.RuntimeType_RUNTIME_TYPE_UNSPECIFIED {
		runtimeType = novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER
	}

	l := layout.New(s.cfg.RootDir)
	sandboxDir := l.SandboxDir(sandboxID)
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sandbox directory: %w", err)
	}

	record := store.SandboxRecord{
		ID:          sandboxID,
		State:       sandbox.StateCreating,
		RuntimeType: runtimeType.String(),
		TemplateID:  req.GetTemplateId(),
		ImageID:     req.GetImageId(),
		SnapshotID:  req.GetSnapshotId(),
	}
	if source := req.GetRootfsSource(); source != nil {
		if runtimeType != novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER {
			return nil, status.Error(codes.InvalidArgument, "overlaybd rootfs requires gvisor runtime")
		}
		if source.GetProvider() != overlaybd.ProviderName || source.GetImage() == "" {
			return nil, status.Error(codes.InvalidArgument, "valid overlaybd rootfs image is required")
		}
		if source.GetPullMode() != "" && source.GetPullMode() != "lazy" {
			return nil, status.Error(codes.InvalidArgument, "overlaybd only supports lazy pull mode")
		}
		record.RootfsProvider = overlaybd.ProviderName
		record.RootfsSourceRef = source.GetImage()
		record.RootfsSnapshotKey = overlaybd.SnapshotKey(sandboxID)
	}
	if err := s.store.CreateSandbox(ctx, record); err != nil {
		if isAlreadyExistsError(err) {
			return nil, status.Error(codes.AlreadyExists, "sandbox already exists")
		}
		return nil, err
	}
	createSucceeded := false
	rootfsPrepared := false
	defer func() {
		if !createSucceeded && rootfsPrepared {
			if err := s.removeOverlayBDRootfs(context.Background(), record); err != nil {
				s.logger.Warn("rollback overlaybd rootfs failed", "sandbox_id", record.ID, "error", err)
			}
		}
	}()
	slot, err := s.assignSandboxNetworkSlot(ctx, record.ID)
	if err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		return nil, err
	}
	record.NetworkSlot = slot
	spec := s.completeRuntimeSpec(record, req.GetRuntimeSpec())
	if err := s.prepareSandboxRuntimeFiles(ctx, record, spec); err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		_ = s.releaseSandboxNetwork(ctx, record)
		return nil, err
	}
	rootfsPrepared = record.RootfsProvider == overlaybd.ProviderName
	if err := ensureSnapshotSpecDirs(spec.Snapshot); err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		_ = s.releaseSandboxNetwork(ctx, record)
		return nil, err
	}
	if err := newSandboxNetworkManager(s.cfg).Prepare(ctx, spec.GetRuntimeType(), spec.GetNetwork()); err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		_ = s.releaseSandboxNetwork(ctx, record)
		return nil, err
	}

	shimSocket := filepath.Join(sandboxDir, "shim.sock")
	if err := ensureShim(ctx, s.cfg, shimSocket, runtimeDriverFromRuntimeType(runtimeType)); err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		_ = s.releaseSandboxNetwork(ctx, record)
		return nil, err
	}

	shim, closeShim, err := dialShim(ctx, shimSocket)
	if err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		_ = s.releaseSandboxNetwork(ctx, record)
		return nil, err
	}
	defer closeShim()

	runtimeInfo, err := shim.CreateRuntime(ctx, &novitaboxv1.CreateRuntimeRequest{RuntimeSpec: spec})
	if err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		_ = s.releaseSandboxNetwork(ctx, record)
		return nil, fmt.Errorf("create runtime: %w", err)
	}
	if err := s.ensureSandboxAgentCurrent(ctx, record.ID, spec.GetNetwork()); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: record.ID})
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		_ = s.releaseSandboxNetwork(ctx, record)
		return nil, fmt.Errorf("refresh sandbox agent: %w", err)
	}

	if err := s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateRunning, "create"); err != nil {
		return nil, err
	}
	createSucceeded = true

	created, err := s.store.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}

	s.logger.Info("created sandbox runtime",
		"sandbox_id", sandboxID,
		"runtime_state", runtimeInfo.GetState().String(),
		"shim_socket", runtimeInfo.GetShimSocketPath(),
	)

	return sandboxRecordToProto(*created, runtimeType), nil
}

func (s *sandboxService) ListSandboxes(ctx context.Context, _ *novitaboxv1.ListSandboxesRequest) (*novitaboxv1.ListSandboxesResponse, error) {
	records, err := s.store.ListSandboxes(ctx)
	if err != nil {
		return nil, err
	}

	resp := &novitaboxv1.ListSandboxesResponse{
		Sandboxes: make([]*novitaboxv1.SandboxInfo, 0, len(records)),
	}
	for _, record := range records {
		resp.Sandboxes = append(resp.Sandboxes, sandboxRecordToProto(record, runtimeTypeFromRecord(record.RuntimeType)))
	}

	return resp, nil
}

func (s *sandboxService) GetSandbox(ctx context.Context, req *novitaboxv1.GetSandboxRequest) (*novitaboxv1.SandboxInfo, error) {
	record, err := s.store.GetSandbox(ctx, req.GetSandboxId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "sandbox not found")
		}
		return nil, err
	}

	return sandboxRecordToProto(*record, runtimeTypeFromRecord(record.RuntimeType)), nil
}

func (s *sandboxService) UpdateSandboxBalloon(ctx context.Context, req *novitaboxv1.UpdateSandboxBalloonRequest) (*novitaboxv1.BalloonConfig, error) {
	shim, closeShim, err := s.dialBalloonSandboxShim(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeShim()
	return shim.UpdateBalloon(ctx, &novitaboxv1.UpdateBalloonRequest{SandboxId: req.GetSandboxId(), AmountMib: req.GetAmountMib()})
}

func (s *sandboxService) GetSandboxBalloon(ctx context.Context, req *novitaboxv1.GetSandboxBalloonRequest) (*novitaboxv1.BalloonConfig, error) {
	shim, closeShim, err := s.dialBalloonSandboxShim(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeShim()
	return shim.GetBalloon(ctx, &novitaboxv1.GetBalloonRequest{SandboxId: req.GetSandboxId()})
}

func (s *sandboxService) GetSandboxBalloonStats(ctx context.Context, req *novitaboxv1.GetSandboxBalloonStatsRequest) (*novitaboxv1.BalloonStats, error) {
	shim, closeShim, err := s.dialBalloonSandboxShim(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeShim()
	return shim.GetBalloonStats(ctx, &novitaboxv1.GetBalloonStatsRequest{SandboxId: req.GetSandboxId()})
}

func (s *sandboxService) UpdateSandboxBalloonStats(ctx context.Context, req *novitaboxv1.UpdateSandboxBalloonStatsRequest) (*novitaboxv1.BalloonConfig, error) {
	shim, closeShim, err := s.dialBalloonSandboxShim(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeShim()
	return shim.UpdateBalloonStats(ctx, &novitaboxv1.UpdateBalloonStatsRequest{
		SandboxId:             req.GetSandboxId(),
		StatsPollingIntervalS: req.GetStatsPollingIntervalS(),
	})
}

func (s *sandboxService) StartSandboxBalloonHinting(ctx context.Context, req *novitaboxv1.StartSandboxBalloonHintingRequest) (*novitaboxv1.BalloonHintingStatus, error) {
	shim, closeShim, err := s.dialBalloonSandboxShim(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeShim()
	return shim.StartBalloonHinting(ctx, &novitaboxv1.StartBalloonHintingRequest{
		SandboxId:         req.GetSandboxId(),
		AcknowledgeOnStop: req.GetAcknowledgeOnStop(),
	})
}

func (s *sandboxService) StopSandboxBalloonHinting(ctx context.Context, req *novitaboxv1.StopSandboxBalloonHintingRequest) (*novitaboxv1.BalloonHintingStatus, error) {
	shim, closeShim, err := s.dialBalloonSandboxShim(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeShim()
	return shim.StopBalloonHinting(ctx, &novitaboxv1.StopBalloonHintingRequest{SandboxId: req.GetSandboxId()})
}

func (s *sandboxService) GetSandboxBalloonHinting(ctx context.Context, req *novitaboxv1.GetSandboxBalloonHintingRequest) (*novitaboxv1.BalloonHintingStatus, error) {
	shim, closeShim, err := s.dialBalloonSandboxShim(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	defer closeShim()
	return shim.GetBalloonHinting(ctx, &novitaboxv1.GetBalloonHintingRequest{SandboxId: req.GetSandboxId()})
}

func (s *sandboxService) dialBalloonSandboxShim(ctx context.Context, sandboxID string) (novitaboxv1.BoxShimClient, func() error, error) {
	if sandboxID == "" {
		return nil, nil, status.Error(codes.InvalidArgument, "sandbox_id is required")
	}
	record, err := s.store.GetSandbox(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, status.Error(codes.NotFound, "sandbox not found")
		}
		return nil, nil, err
	}
	shim, closeShim, err := s.dialSandboxShim(ctx, sandboxID)
	if err != nil {
		return nil, nil, err
	}
	caps, err := shim.Capabilities(ctx, &novitaboxv1.CapabilitiesRequest{RuntimeType: runtimeTypeFromRecord(record.RuntimeType)})
	if err != nil {
		_ = closeShim()
		return nil, nil, fmt.Errorf("get runtime capabilities: %w", err)
	}
	if !caps.GetBalloon() {
		_ = closeShim()
		runtimeName := runtimeDriverFromRecord(record.RuntimeType)
		if runtimeName == "" {
			runtimeName = "unknown"
		}
		return nil, nil, status.Errorf(codes.Unimplemented, "balloon is not supported by runtime %q", runtimeName)
	}
	return shim, closeShim, nil
}

func (s *sandboxService) PauseSandbox(ctx context.Context, req *novitaboxv1.PauseSandboxRequest) (*novitaboxv1.SnapshotInfo, error) {
	record, err := s.store.GetSandbox(ctx, req.GetSandboxId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "sandbox not found")
		}
		return nil, err
	}

	if err := s.setSandboxState(ctx, record.ID, sandbox.StatePausing, "pause"); err != nil {
		return nil, err
	}

	shim, closeShim, err := s.dialSandboxShim(ctx, record.ID)
	if err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "pause")
		return nil, err
	}
	defer closeShim()

	if _, err := shim.PauseRuntime(ctx, &novitaboxv1.PauseRuntimeRequest{SandboxId: record.ID}); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "pause")
		return nil, fmt.Errorf("pause runtime: %w", err)
	}

	snapshot := sandboxSnapshotRecord(s.cfg.RootDir, record.ID)
	if err := s.store.CreateSnapshot(ctx, snapshot); err != nil {
		return nil, err
	}
	if err := s.setSandboxState(ctx, record.ID, sandbox.StatePaused, "pause"); err != nil {
		return nil, err
	}
	if err := s.releaseSandboxNetwork(ctx, *record); err != nil {
		return nil, err
	}

	return snapshotRecordToProto(snapshot), nil
}

func (s *sandboxService) ResumeSandbox(ctx context.Context, req *novitaboxv1.ResumeSandboxRequest) (*novitaboxv1.SandboxInfo, error) {
	record, err := s.store.GetSandbox(ctx, req.GetSandboxId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "sandbox not found")
		}
		return nil, err
	}
	if err := s.setSandboxState(ctx, record.ID, sandbox.StateResuming, "resume"); err != nil {
		return nil, err
	}
	slot, err := s.assignSandboxNetworkSlot(ctx, record.ID)
	if err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "resume")
		return nil, err
	}
	record.NetworkSlot = slot

	shim, closeShim, err := s.dialSandboxShim(ctx, record.ID)
	if err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "resume")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, err
	}
	defer closeShim()

	spec := s.runtimeSpecForSandbox(*record)
	if err := s.attachAgentDrive(ctx, spec); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "resume")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, err
	}
	if err := newSandboxNetworkManager(s.cfg).Prepare(ctx, spec.GetRuntimeType(), spec.GetNetwork()); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "resume")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, err
	}
	if _, err := shim.ResumeRuntime(ctx, &novitaboxv1.ResumeRuntimeRequest{RuntimeSpec: spec}); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "resume")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, fmt.Errorf("resume runtime: %w", err)
	}
	if err := s.ensureSandboxAgentCurrent(ctx, record.ID, spec.GetNetwork()); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "resume")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, fmt.Errorf("refresh sandbox agent: %w", err)
	}
	if err := s.setSandboxState(ctx, record.ID, sandbox.StateRunning, "resume"); err != nil {
		return nil, err
	}

	updated, err := s.store.GetSandbox(ctx, record.ID)
	if err != nil {
		return nil, err
	}

	return sandboxRecordToProto(*updated, runtimeTypeFromRecord(updated.RuntimeType)), nil
}

func (s *sandboxService) KillSandbox(ctx context.Context, req *novitaboxv1.KillSandboxRequest) (*emptypb.Empty, error) {
	record, err := s.store.GetSandbox(ctx, req.GetSandboxId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "sandbox not found")
		}
		return nil, err
	}

	if err := s.setSandboxState(ctx, record.ID, sandbox.StateKilling, "kill"); err != nil {
		return nil, err
	}
	sandboxDir := layout.New(s.cfg.RootDir).SandboxDir(record.ID)
	if shim, closeShim, err := s.dialSandboxShim(ctx, record.ID); err == nil {
		_, _ = shim.KillRuntime(ctx, &novitaboxv1.KillRuntimeRequest{SandboxId: record.ID})
		_ = closeShim()
	}
	if err := terminateShimProcess(sandboxDir, 5*time.Second); err != nil {
		s.logger.Warn("terminate sandbox shim failed", "sandbox_id", record.ID, "error", err)
	}
	if err := s.removeOverlayBDRootfs(ctx, *record); err != nil {
		return nil, err
	}
	if err := s.setSandboxState(ctx, record.ID, sandbox.StateKilled, "kill"); err != nil {
		return nil, err
	}
	if err := s.deleteSandboxSnapshots(ctx, record.ID); err != nil {
		return nil, err
	}
	if err := s.store.DeleteSandbox(ctx, record.ID); err != nil {
		return nil, err
	}
	if err := removeSandboxDirectory(sandboxDir); err != nil {
		return nil, fmt.Errorf("remove sandbox directory: %w", err)
	}
	if err := s.releaseSandboxNetwork(ctx, *record); err != nil {
		s.logger.Warn("cleanup sandbox network failed", "sandbox_id", record.ID, "error", err)
	}

	return &emptypb.Empty{}, nil
}

func (s *sandboxService) deleteSandboxSnapshots(ctx context.Context, sandboxID string) error {
	snapshots, err := s.store.ListSnapshotsBySandbox(ctx, sandboxID)
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		if err := s.store.DeleteSnapshot(ctx, snapshot.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}

	return nil
}

func (s *sandboxService) StopSandbox(ctx context.Context, req *novitaboxv1.StopSandboxRequest) (*novitaboxv1.SandboxInfo, error) {
	info, err := s.runtimeAction(ctx, req.GetSandboxId(), sandbox.StateStopping, sandbox.StateStopped, "stop", func(shim novitaboxv1.BoxShimClient, sandboxID string) error {
		_, err := shim.StopRuntime(ctx, &novitaboxv1.StopRuntimeRequest{SandboxId: sandboxID, TimeoutSeconds: req.GetTimeoutSeconds()})
		return err
	})
	if err != nil {
		return nil, err
	}
	record, err := s.store.GetSandbox(ctx, req.GetSandboxId())
	if err != nil {
		return nil, err
	}
	if err := s.unmountOverlayBDRootfs(ctx, *record); err != nil {
		return nil, err
	}
	return info, nil
}

func (s *sandboxService) StartSandbox(ctx context.Context, req *novitaboxv1.StartSandboxRequest) (*novitaboxv1.SandboxInfo, error) {
	record, err := s.store.GetSandbox(ctx, req.GetSandboxId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "sandbox not found")
		}
		return nil, err
	}
	if err := s.setSandboxState(ctx, record.ID, sandbox.StateStarting, "start"); err != nil {
		return nil, err
	}
	slot, err := s.assignSandboxNetworkSlot(ctx, record.ID)
	if err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
		return nil, err
	}
	record.NetworkSlot = slot
	mountedOverlayBD := false
	if record.RootfsProvider == overlaybd.ProviderName {
		if err := s.mountOverlayBDRootfs(ctx, *record); err != nil {
			_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
			_ = s.releaseSandboxNetwork(ctx, *record)
			return nil, err
		}
		mountedOverlayBD = true
	}
	startSucceeded := false
	defer func() {
		if mountedOverlayBD && !startSucceeded {
			_ = s.unmountOverlayBDRootfs(context.Background(), *record)
		}
	}()

	shim, closeShim, err := s.dialSandboxShim(ctx, record.ID)
	if err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, err
	}
	defer closeShim()

	spec := s.runtimeSpecForSandbox(*record)
	if err := s.attachAgentDrive(ctx, spec); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, err
	}
	if err := newSandboxNetworkManager(s.cfg).Prepare(ctx, spec.GetRuntimeType(), spec.GetNetwork()); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, err
	}
	if _, err := shim.StartRuntime(ctx, &novitaboxv1.StartRuntimeRequest{RuntimeSpec: spec}); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, fmt.Errorf("start runtime: %w", err)
	}
	if err := s.ensureSandboxAgentCurrent(ctx, record.ID, spec.GetNetwork()); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
		_ = s.releaseSandboxNetwork(ctx, *record)
		return nil, fmt.Errorf("refresh sandbox agent: %w", err)
	}
	if err := s.setSandboxState(ctx, record.ID, sandbox.StateRunning, "start"); err != nil {
		return nil, err
	}
	startSucceeded = true

	updated, err := s.store.GetSandbox(ctx, record.ID)
	if err != nil {
		return nil, err
	}

	return sandboxRecordToProto(*updated, runtimeTypeFromRecord(updated.RuntimeType)), nil
}

func (s *sandboxService) RebootSandbox(ctx context.Context, req *novitaboxv1.RebootSandboxRequest) (*novitaboxv1.SandboxInfo, error) {
	return s.runtimeAction(ctx, req.GetSandboxId(), sandbox.StateRebooting, sandbox.StateRunning, "reboot", func(shim novitaboxv1.BoxShimClient, sandboxID string) error {
		_, err := shim.RebootRuntime(ctx, &novitaboxv1.RebootRuntimeRequest{SandboxId: sandboxID, TimeoutSeconds: req.GetTimeoutSeconds()})
		return err
	})
}

func (s *sandboxService) runtimeAction(ctx context.Context, sandboxID string, transitionState sandbox.State, finalState sandbox.State, action string, call func(novitaboxv1.BoxShimClient, string) error) (*novitaboxv1.SandboxInfo, error) {
	record, err := s.store.GetSandbox(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "sandbox not found")
		}
		return nil, err
	}
	if err := s.setSandboxState(ctx, record.ID, transitionState, action); err != nil {
		return nil, err
	}

	shim, closeShim, err := s.dialSandboxShim(ctx, record.ID)
	if err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, action)
		return nil, err
	}
	defer closeShim()

	if err := call(shim, record.ID); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, action)
		return nil, fmt.Errorf("%s runtime: %w", action, err)
	}
	if err := s.setSandboxState(ctx, record.ID, finalState, action); err != nil {
		return nil, err
	}
	if finalState == sandbox.StateStopped || finalState == sandbox.StatePaused || finalState == sandbox.StateKilled {
		if err := s.releaseSandboxNetwork(ctx, *record); err != nil {
			return nil, err
		}
	}

	updated, err := s.store.GetSandbox(ctx, record.ID)
	if err != nil {
		return nil, err
	}

	return sandboxRecordToProto(*updated, runtimeTypeFromRecord(updated.RuntimeType)), nil
}

func (s *sandboxService) dialSandboxShim(ctx context.Context, sandboxID string) (novitaboxv1.BoxShimClient, func() error, error) {
	return dialShim(ctx, filepath.Join(layout.New(s.cfg.RootDir).SandboxDir(sandboxID), "shim.sock"))
}

func (s *sandboxService) setSandboxState(ctx context.Context, sandboxID string, to sandbox.State, action string) error {
	record, err := s.store.GetSandbox(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return status.Error(codes.NotFound, "sandbox not found")
		}
		return err
	}
	if record.State == to {
		return nil
	}

	if err := s.store.UpdateSandboxState(ctx, sandboxID, record.State, to, action); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return status.Error(codes.NotFound, "sandbox not found")
		}
		return err
	}
	return nil
}

func (s *sandboxService) assignSandboxNetworkSlot(ctx context.Context, sandboxID string) (uint32, error) {
	if !s.cfg.Network.Enabled {
		return 0, nil
	}
	manager := newSandboxNetworkManager(s.cfg)
	maxSlot, err := manager.MaxSlots()
	if err != nil {
		return 0, err
	}
	return s.store.AssignSandboxNetworkSlot(ctx, sandboxID, maxSlot)
}

func (s *sandboxService) releaseSandboxNetwork(ctx context.Context, record store.SandboxRecord) error {
	if !s.cfg.Network.Enabled {
		return nil
	}
	if record.NetworkSlot > 0 {
		if err := newSandboxNetworkManager(s.cfg).Cleanup(ctx, record.ID, record.NetworkSlot); err != nil {
			return err
		}
	}
	if err := s.store.ReleaseSandboxNetworkSlot(ctx, record.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

func (s *sandboxService) ensureSandboxAgentCurrent(ctx context.Context, sandboxID string, networkSpec *novitaboxv1.NetworkSpec) error {
	if strings.EqualFold(s.cfg.Boxshim.RuntimeDriver, "stub") || !s.cfg.Network.Enabled {
		return nil
	}
	if networkSpec == nil || networkSpec.GetHostAccessIp() == "" {
		return errors.New("sandbox network host_access_ip is required to refresh agent")
	}
	_, port, err := net.SplitHostPort(s.cfg.Boxd.Addr)
	if err != nil {
		return fmt.Errorf("parse boxd address %q: %w", s.cfg.Boxd.Addr, err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	baseURL := "http://" + net.JoinHostPort(networkSpec.GetHostAccessIp(), port)
	healthURL := baseURL + "/healthz"
	if err := waitHTTPHealthWithClient(ctx, healthURL, time.Duration(s.cfg.Template.AgentWaitSecs)*time.Second, client); err != nil {
		return err
	}
	beforeStartedAt, err := readAgentStartedAt(ctx, healthURL, client)
	if err != nil {
		return err
	}

	body := map[string]any{
		"path":        s.cfg.Template.BoxdGuestPath,
		"mountDevice": "/dev/vdb",
		"mountPath":   "/novitabox/agent",
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal agent reexec request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/admin/reexec", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create agent reexec request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("call agent reexec: %w", err)
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("agent reexec returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if err := waitAgentReexecComplete(ctx, healthURL, beforeStartedAt, time.Duration(s.cfg.Template.AgentWaitSecs)*time.Second, client); err != nil {
		return fmt.Errorf("wait for refreshed agent health: %w", err)
	}

	s.logger.Info("refreshed sandbox agent", "sandbox_id", sandboxID, "agent_path", s.cfg.Template.BoxdGuestPath)
	return nil
}

func readAgentStartedAt(ctx context.Context, healthURL string, client *http.Client) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create health request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return 0, fmt.Errorf("health endpoint returned status %d", resp.StatusCode)
	}

	var body struct {
		StartedAtUnixNano int64 `json:"startedAtUnixNano"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); err != nil {
		return 0, fmt.Errorf("decode health response: %w", err)
	}
	if body.StartedAtUnixNano == 0 {
		return 0, errors.New("health response missing startedAtUnixNano")
	}
	return body.StartedAtUnixNano, nil
}

func waitAgentReexecComplete(ctx context.Context, healthURL string, previousStartedAt int64, timeout time.Duration, client *http.Client) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		startedAt, err := readAgentStartedAt(ctx, healthURL, client)
		if err == nil && startedAt != previousStartedAt {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = errors.New("agent has not reexeced yet")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return lastErr
}

func isAlreadyExistsError(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *sandboxService) runtimeSpecForSandbox(record store.SandboxRecord) *novitaboxv1.RuntimeSpec {
	paths := sandboxRuntimePaths(s.cfg.RootDir, record.ID)
	var networkSpec *novitaboxv1.NetworkSpec
	if record.NetworkSlot > 0 {
		networkSpec, _ = newSandboxNetworkManager(s.cfg).SpecForSlot(record.ID, record.NetworkSlot)
	}
	runtimeType := runtimeTypeFromRecord(record.RuntimeType)
	rootfsPath := paths.RootfsPath
	rootfsFormat := "ext4"
	if runtimeType == novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER {
		rootfsPath = filepath.Join(layout.New(s.cfg.RootDir).SandboxDir(record.ID), "rootfs")
		rootfsFormat = "dir"
	}
	return &novitaboxv1.RuntimeSpec{
		SandboxId:   record.ID,
		RuntimeType: runtimeType,
		Machine: &novitaboxv1.MachineSpec{
			Vcpu:     1,
			MemoryMb: 512,
		},
		Rootfs: &novitaboxv1.RootfsSpec{
			Path:   rootfsPath,
			Format: rootfsFormat,
		},
		Network: networkSpec,
		Kernel: func() *novitaboxv1.KernelSpec {
			if runtimeType != novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER {
				return nil
			}
			return &novitaboxv1.KernelSpec{
				KernelPath: paths.KernelPath,
				KernelArgs: templateKernelArgs(s.cfg.Template.KernelArgs),
			}
		}(),
		Snapshot: func() *novitaboxv1.SnapshotSpec {
			if runtimeType != novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER {
				return nil
			}
			return &novitaboxv1.SnapshotSpec{
				MemfilePath:  paths.MemfilePath,
				SnapfilePath: paths.SnapfilePath,
				SnapshotType: "full",
			}
		}(),
	}
}

func (s *sandboxService) completeRuntimeSpec(record store.SandboxRecord, spec *novitaboxv1.RuntimeSpec) *novitaboxv1.RuntimeSpec {
	if spec == nil {
		spec = s.runtimeSpecForSandbox(record)
	}
	paths := sandboxRuntimePaths(s.cfg.RootDir, record.ID)
	runtimeType := runtimeTypeFromRecord(record.RuntimeType)
	spec.SandboxId = record.ID
	spec.RuntimeType = runtimeType
	if spec.Machine == nil {
		spec.Machine = &novitaboxv1.MachineSpec{}
	}
	if spec.Machine.Vcpu == 0 {
		spec.Machine.Vcpu = s.cfg.Template.VCPU
	}
	if spec.Machine.MemoryMb == 0 {
		spec.Machine.MemoryMb = s.cfg.Template.MemoryMB
	}
	if spec.Rootfs == nil {
		spec.Rootfs = &novitaboxv1.RootfsSpec{}
	}
	if spec.Rootfs.Path == "" {
		if runtimeType == novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER {
			spec.Rootfs.Path = filepath.Join(layout.New(s.cfg.RootDir).SandboxDir(record.ID), "rootfs")
			spec.Rootfs.Format = "dir"
		} else {
			spec.Rootfs.Path = paths.RootfsPath
			spec.Rootfs.Format = "ext4"
		}
	}
	if runtimeType == novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER {
		if spec.Kernel == nil {
			spec.Kernel = &novitaboxv1.KernelSpec{}
		}
		if spec.Kernel.KernelPath == "" {
			spec.Kernel.KernelPath = paths.KernelPath
		}
		if len(spec.Kernel.KernelArgs) == 0 {
			spec.Kernel.KernelArgs = templateKernelArgs(s.cfg.Template.KernelArgs)
		}
		if spec.Snapshot == nil {
			spec.Snapshot = &novitaboxv1.SnapshotSpec{
				MemfilePath:  paths.MemfilePath,
				SnapfilePath: paths.SnapfilePath,
				SnapshotType: "full",
			}
		}
		if spec.Snapshot.MemfilePath == "" {
			spec.Snapshot.MemfilePath = paths.MemfilePath
		}
		if spec.Snapshot.SnapfilePath == "" {
			spec.Snapshot.SnapfilePath = paths.SnapfilePath
		}
	} else {
		spec.Kernel = nil
		spec.Snapshot = nil
	}
	if record.NetworkSlot > 0 {
		if networkSpec, err := newSandboxNetworkManager(s.cfg).Complete(record.ID, record.NetworkSlot, spec.Network); err == nil {
			spec.Network = networkSpec
		}
	}
	if spec.Agent == nil {
		spec.Agent = &novitaboxv1.AgentSpec{
			Type:     "boxd",
			Protocol: "grpc",
			Port:     49983,
		}
	}
	return spec
}

func (s *sandboxService) prepareSandboxRuntimeFiles(ctx context.Context, record store.SandboxRecord, spec *novitaboxv1.RuntimeSpec) error {
	if err := s.attachAgentDrive(ctx, spec); err != nil {
		return err
	}
	if spec.GetKernel().GetKernelPath() != "" {
		if s.cfg.Template.KernelPath == "" {
			return errors.New("sandbox runtime requires --template-kernel")
		}
		if err := linkOrCopyFile(s.cfg.Template.KernelPath, spec.GetKernel().GetKernelPath()); err != nil {
			return fmt.Errorf("prepare sandbox kernel: %w", err)
		}
	}
	if record.RootfsProvider == overlaybd.ProviderName {
		provider, closeProvider, err := s.overlayBDProvider()
		if err != nil {
			return err
		}
		defer closeProvider()
		handle, err := provider.Prepare(ctx, overlaybd.PrepareRequest{
			SandboxID: record.ID,
			SourceRef: record.RootfsSourceRef,
			Target:    spec.GetRootfs().GetPath(),
		})
		if err != nil {
			return err
		}
		if handle.SourceDigest != "" {
			if err := s.store.UpdateSandboxRootfsDigest(ctx, record.ID, handle.SourceDigest); err != nil {
				_ = provider.Remove(context.Background(), handle)
				return fmt.Errorf("persist overlaybd image digest: %w", err)
			}
		}
		if err := injectGVisorBoxdBinary(s.cfg, handle.Target); err != nil {
			_ = provider.Remove(context.Background(), handle)
			return err
		}
		return nil
	}
	if record.TemplateID != "" {
		template, err := s.store.GetTemplate(ctx, record.TemplateID)
		if err != nil {
			return fmt.Errorf("get template %q: %w", record.TemplateID, err)
		}
		if err := cloneOrCopyPath(template.RootfsPath, spec.GetRootfs().GetPath()); err != nil {
			return fmt.Errorf("prepare sandbox rootfs from template %q: %w", record.TemplateID, err)
		}
		if template.MemfilePath != "" && spec.GetSnapshot().GetMemfilePath() != "" {
			if err := cloneOrCopyFile(template.MemfilePath, spec.GetSnapshot().GetMemfilePath()); err != nil {
				return fmt.Errorf("prepare sandbox memfile from template %q: %w", record.TemplateID, err)
			}
		}
		if template.SnapfilePath != "" && spec.GetSnapshot().GetSnapfilePath() != "" {
			if err := cloneOrCopyFile(template.SnapfilePath, spec.GetSnapshot().GetSnapfilePath()); err != nil {
				return fmt.Errorf("prepare sandbox snapfile from template %q: %w", record.TemplateID, err)
			}
		}
		return nil
	}
	if record.ImageID != "" {
		image, err := s.store.GetImage(ctx, record.ImageID)
		if err != nil {
			return fmt.Errorf("get image %q: %w", record.ImageID, err)
		}
		if err := cloneOrCopyPath(image.RootfsPath, spec.GetRootfs().GetPath()); err != nil {
			return fmt.Errorf("prepare sandbox rootfs from image %q: %w", record.ImageID, err)
		}
		return nil
	}

	return nil
}

func (s *sandboxService) overlayBDHandle(record store.SandboxRecord) overlaybd.Handle {
	return overlaybd.Handle{
		SnapshotKey: record.RootfsSnapshotKey,
		SourceRef:   record.RootfsSourceRef,
		Target:      filepath.Join(layout.New(s.cfg.RootDir).SandboxDir(record.ID), "rootfs"),
	}
}

func (s *sandboxService) mountOverlayBDRootfs(ctx context.Context, record store.SandboxRecord) error {
	if record.RootfsProvider != overlaybd.ProviderName {
		return nil
	}
	provider, closeProvider, err := s.overlayBDProvider()
	if err != nil {
		return err
	}
	defer closeProvider()
	if err := provider.Mount(ctx, s.overlayBDHandle(record)); err != nil {
		return fmt.Errorf("mount sandbox overlaybd rootfs: %w", err)
	}
	return nil
}

func (s *sandboxService) unmountOverlayBDRootfs(ctx context.Context, record store.SandboxRecord) error {
	if record.RootfsProvider != overlaybd.ProviderName {
		return nil
	}
	provider, closeProvider, err := s.overlayBDProvider()
	if err != nil {
		return err
	}
	defer closeProvider()
	if err := provider.Unmount(ctx, s.overlayBDHandle(record)); err != nil {
		return fmt.Errorf("unmount sandbox overlaybd rootfs: %w", err)
	}
	return nil
}

func (s *sandboxService) removeOverlayBDRootfs(ctx context.Context, record store.SandboxRecord) error {
	if record.RootfsProvider != overlaybd.ProviderName || record.RootfsSnapshotKey == "" {
		return nil
	}
	provider, closeProvider, err := s.overlayBDProvider()
	if err != nil {
		return err
	}
	defer closeProvider()
	if err := provider.Remove(ctx, s.overlayBDHandle(record)); err != nil {
		return fmt.Errorf("remove sandbox overlaybd rootfs: %w", err)
	}
	return nil
}

func ensureShim(ctx context.Context, cfg config.Config, socketPath string, runtimeDriver string) error {
	if _, err := os.Stat(socketPath); err == nil {
		if err := waitShimReady(ctx, socketPath, 500*time.Millisecond); err == nil {
			return nil
		}
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("remove stale boxshim socket %q: %w", socketPath, err)
		}
	}

	shimBin, err := resolveShimBinary(cfg.Boxshim.BinaryPath)
	if err != nil {
		return err
	}
	if runtimeDriver == "" {
		runtimeDriver = cfg.Boxshim.RuntimeDriver
	}
	cmd := exec.Command(
		shimBin,
		"--root", cfg.RootDir,
		"--socket", socketPath,
		"--runtime-driver", runtimeDriver,
		"--firecracker-bin", cfg.Firecracker.BinaryPath,
		"--runsc-bin", cfg.GVisor.RunscBinaryPath,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start boxshim: %w", err)
	}

	pidPath := filepath.Join(filepath.Dir(socketPath), "shim.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644); err != nil {
		return fmt.Errorf("write shim pid: %w", err)
	}
	go func() {
		_ = cmd.Wait()
	}()

	if err := waitShimReady(ctx, socketPath, 5*time.Second); err != nil {
		return fmt.Errorf("wait for boxshim %q ready: %w", socketPath, err)
	}
	return nil
}

func terminateShimProcess(sandboxDir string, timeout time.Duration) error {
	pidPath := filepath.Join(sandboxDir, "shim.pid")
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read shim pid: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("parse shim pid %q: %w", strings.TrimSpace(string(raw)), err)
	}

	if waitProcessGone(pid, timeout) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("terminate shim pid %d: %w", pid, err)
	}
	if waitProcessGone(pid, timeout) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill shim pid %d: %w", pid, err)
	}
	return nil
}

func waitProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return errors.Is(err, syscall.ESRCH)
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func resolveShimBinary(path string) (string, error) {
	if path == "" {
		path = "boxshim"
	}
	if filepath.IsAbs(path) {
		return path, nil
	}

	bin, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find boxlet executable: %w", err)
	}

	return filepath.Join(filepath.Dir(bin), path), nil
}

func dialShim(ctx context.Context, socketPath string) (novitaboxv1.BoxShimClient, func() error, error) {
	conn, err := grpc.NewClient("unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create shim client: %w", err)
	}

	return novitaboxv1.NewBoxShimClient(conn), conn.Close, nil
}

func waitShimReady(ctx context.Context, socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		shim, closeShim, err := dialShim(ctx, socketPath)
		if err == nil {
			probeCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			_, err = shim.Capabilities(probeCtx, &novitaboxv1.CapabilitiesRequest{
				RuntimeType: novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER,
			})
			cancel()
			_ = closeShim()
			if err == nil {
				return nil
			}
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("timed out after %s", timeout)
}

func sandboxRecordToProto(record store.SandboxRecord, runtimeType novitaboxv1.RuntimeType) *novitaboxv1.SandboxInfo {
	info := &novitaboxv1.SandboxInfo{
		SandboxId:     record.ID,
		State:         sandboxStateToProto(record.State),
		RuntimeType:   runtimeType,
		TemplateId:    record.TemplateID,
		ImageId:       record.ImageID,
		SnapshotId:    record.SnapshotID,
		CreatedAtUnix: record.CreatedAt.Unix(),
		UpdatedAtUnix: record.UpdatedAt.Unix(),
	}
	if record.RootfsProvider != "" && (record.RootfsProvider != "directory" || record.RootfsSourceRef != "" || record.RootfsSourceDigest != "" || record.RootfsSnapshotKey != "") {
		info.Rootfs = &novitaboxv1.RootfsInfo{
			Provider:    record.RootfsProvider,
			Image:       record.RootfsSourceRef,
			Digest:      record.RootfsSourceDigest,
			SnapshotKey: record.RootfsSnapshotKey,
		}
	}
	return info
}

type sandboxRuntimeArtifactPaths struct {
	KernelPath   string
	RootfsPath   string
	MemfilePath  string
	SnapfilePath string
}

func sandboxRuntimePaths(rootDir string, sandboxID string) sandboxRuntimeArtifactPaths {
	sandboxDir := layout.New(rootDir).SandboxDir(sandboxID)
	snapshotDir := filepath.Join(sandboxDir, "snapshot")
	return sandboxRuntimeArtifactPaths{
		KernelPath:   filepath.Join(sandboxDir, "kernel"),
		RootfsPath:   filepath.Join(snapshotDir, "rootfs.ext4"),
		MemfilePath:  filepath.Join(snapshotDir, "memfile"),
		SnapfilePath: filepath.Join(snapshotDir, "snapfile"),
	}
}

func sandboxSnapshotRecord(rootDir string, sandboxID string) store.SnapshotRecord {
	paths := sandboxRuntimePaths(rootDir, sandboxID)
	now := time.Now()
	return store.SnapshotRecord{
		ID:           fmt.Sprintf("snap-%s-%d", sandboxID, time.Now().UnixNano()),
		SandboxID:    sandboxID,
		RootfsPath:   paths.RootfsPath,
		MemfilePath:  paths.MemfilePath,
		SnapfilePath: paths.SnapfilePath,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func ensureSnapshotSpecDirs(spec *novitaboxv1.SnapshotSpec) error {
	for _, path := range []string{spec.GetMemfilePath(), spec.GetSnapfilePath()} {
		if path == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create snapshot directory for %q: %w", path, err)
		}
	}

	return nil
}

func snapshotRecordToProto(record store.SnapshotRecord) *novitaboxv1.SnapshotInfo {
	return &novitaboxv1.SnapshotInfo{
		SnapshotId:    record.ID,
		SandboxId:     record.SandboxID,
		RootfsPath:    record.RootfsPath,
		MemfilePath:   record.MemfilePath,
		SnapfilePath:  record.SnapfilePath,
		CreatedAtUnix: record.CreatedAt.Unix(),
	}
}

func sandboxStateToProto(state sandbox.State) novitaboxv1.SandboxState {
	switch state {
	case sandbox.StateCreating:
		return novitaboxv1.SandboxState_SANDBOX_STATE_CREATING
	case sandbox.StateRunning:
		return novitaboxv1.SandboxState_SANDBOX_STATE_RUNNING
	case sandbox.StatePausing:
		return novitaboxv1.SandboxState_SANDBOX_STATE_PAUSING
	case sandbox.StatePaused:
		return novitaboxv1.SandboxState_SANDBOX_STATE_PAUSED
	case sandbox.StateResuming:
		return novitaboxv1.SandboxState_SANDBOX_STATE_RESUMING
	case sandbox.StateStopping:
		return novitaboxv1.SandboxState_SANDBOX_STATE_STOPPING
	case sandbox.StateStopped:
		return novitaboxv1.SandboxState_SANDBOX_STATE_STOPPED
	case sandbox.StateStarting:
		return novitaboxv1.SandboxState_SANDBOX_STATE_STARTING
	case sandbox.StateRebooting:
		return novitaboxv1.SandboxState_SANDBOX_STATE_REBOOTING
	case sandbox.StateKilling:
		return novitaboxv1.SandboxState_SANDBOX_STATE_KILLING
	case sandbox.StateKilled:
		return novitaboxv1.SandboxState_SANDBOX_STATE_KILLED
	case sandbox.StateFailed:
		return novitaboxv1.SandboxState_SANDBOX_STATE_FAILED
	case sandbox.StateUnknown:
		return novitaboxv1.SandboxState_SANDBOX_STATE_UNKNOWN
	default:
		return novitaboxv1.SandboxState_SANDBOX_STATE_UNSPECIFIED
	}
}

func runtimeTypeFromRecord(runtimeType string) novitaboxv1.RuntimeType {
	switch strings.ToLower(runtimeType) {
	case "runtime_type_cloud_hypervisor", "cloud-hypervisor", "cloud_hypervisor":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR
	case "runtime_type_container", "container", "gvisor":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER
	case "runtime_type_firecracker", "firecracker", "":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER
	default:
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER
	}
}

func runtimeDriverFromRuntimeType(runtimeType novitaboxv1.RuntimeType) string {
	switch runtimeType {
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER:
		return "gvisor"
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR:
		return "cloud-hypervisor"
	default:
		return "firecracker"
	}
}

func runtimeDriverFromRecord(runtimeType string) string {
	switch strings.ToLower(strings.TrimSpace(runtimeType)) {
	case "stub":
		return "stub"
	case "runtime_type_container", "container", "gvisor":
		return "gvisor"
	case "runtime_type_cloud_hypervisor", "cloud-hypervisor", "cloud_hypervisor":
		return "cloud-hypervisor"
	case "runtime_type_firecracker", "firecracker", "":
		return "firecracker"
	default:
		return strings.TrimSpace(runtimeType)
	}
}

func runtimeTypeFromRuntimeDriver(runtimeDriver string) novitaboxv1.RuntimeType {
	switch strings.ToLower(strings.TrimSpace(runtimeDriver)) {
	case "gvisor", "container", "runtime_type_container":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER
	case "cloud-hypervisor", "cloud_hypervisor", "runtime_type_cloud_hypervisor":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR
	default:
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER
	}
}

type artifactService struct {
	novitaboxv1.UnimplementedBoxletArtifactServiceServer
	cfg    config.Config
	logger *log.Logger
	store  store.Store
}

func newArtifactService(cfg config.Config, logger *log.Logger, store store.Store) *artifactService {
	return &artifactService{cfg: cfg, logger: logger, store: store}
}

func (s *artifactService) templateRuntimeDriver(ctx context.Context, templateID string) (string, error) {
	runtimeDriver := runtimeDriverFromRecord(s.cfg.Boxshim.RuntimeDriver)
	if s.store == nil {
		return runtimeDriver, nil
	}
	record, err := s.store.GetTemplate(ctx, templateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return runtimeDriver, nil
		}
		return "", err
	}
	if record.Metadata != nil {
		if configured := strings.TrimSpace(record.Metadata["runtimeType"]); configured != "" {
			return runtimeDriverFromRecord(configured), nil
		}
	}
	if record.RootfsPath != "" && record.MemfilePath == "" && record.SnapfilePath == "" {
		return "gvisor", nil
	}
	return runtimeDriver, nil
}

func (s *artifactService) CreateTemplate(ctx context.Context, req *novitaboxv1.CreateTemplateRequest) (*novitaboxv1.TemplateInfo, error) {
	templateID := req.GetTemplateId()
	if templateID == "" {
		return nil, errors.New("template_id is required")
	}
	runtimeDriver, err := s.templateRuntimeDriver(ctx, templateID)
	if err != nil {
		return nil, err
	}
	s.logger.Info("starting template artifact build",
		"template_id", templateID,
		"runtime_driver", runtimeDriver,
		"docker_image", req.GetDockerImage(),
		"from_template", req.GetFromTemplate(),
		"image_id", req.GetImageId(),
		"sandbox_id", req.GetSandboxId(),
	)

	l := layout.New(s.cfg.RootDir)
	templateDir := l.TemplateDir(templateID)
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create template directory: %w", err)
	}

	paths, err := s.templateArtifactPaths(ctx, templateID, templateDir, runtimeDriver)
	if err != nil {
		return nil, err
	}
	s.logger.Info("materializing template rootfs", "template_id", templateID, "rootfs_path", paths.RootfsPath)
	if err := s.materializeTemplateRootfs(ctx, req, paths.RootfsPath, runtimeDriver); err != nil {
		s.logger.Error("materialize template rootfs failed", "template_id", templateID, "error", err)
		return nil, err
	}

	s.logger.Info("injecting template runtime files", "template_id", templateID)
	if err := s.injectTemplateRuntimeFiles(ctx, paths, runtimeDriver); err != nil {
		s.logger.Error("inject template runtime files failed", "template_id", templateID, "error", err)
		return nil, err
	}
	if isGVisorRuntimeDriver(runtimeDriver) {
		s.logger.Info("creating gvisor template", "template_id", templateID, "rootfs_path", paths.RootfsPath)
		if err := s.createGVisorTemplate(ctx, req, templateID, paths, runtimeDriver); err != nil {
			s.logger.Error("create gvisor template failed", "template_id", templateID, "error", err)
			return nil, err
		}
	} else {
		s.logger.Info("creating template snapshot", "template_id", templateID, "memfile_path", paths.MemfilePath, "snapfile_path", paths.SnapfilePath)
		if err := s.createTemplateSnapshot(ctx, req, templateID, paths, runtimeDriver); err != nil {
			s.logger.Error("create template snapshot failed", "template_id", templateID, "error", err)
			return nil, err
		}
	}

	record := store.TemplateRecord{
		ID:           templateID,
		RootfsPath:   paths.RootfsPath,
		MemfilePath:  paths.MemfilePath,
		SnapfilePath: paths.SnapfilePath,
		Metadata: map[string]string{
			"runtimeType": runtimeDriver,
		},
	}
	if err := s.store.CreateTemplate(ctx, record); err != nil {
		existing, getErr := s.store.GetTemplate(ctx, templateID)
		if getErr != nil {
			return nil, err
		}
		record.CreatedAt = existing.CreatedAt
		record.UpdatedAt = existing.UpdatedAt
	}

	s.logger.Info("created template artifact",
		"template_id", templateID,
		"rootfs_path", paths.RootfsPath,
	)

	return templateRecordToProto(record), nil
}

func (s *artifactService) injectTemplateInit(ctx context.Context, rootfsPath string) error {
	if strings.EqualFold(s.cfg.Boxshim.RuntimeDriver, "stub") {
		return nil
	}

	workDir, err := os.MkdirTemp(filepath.Dir(rootfsPath), ".init-inject-*")
	if err != nil {
		return fmt.Errorf("create init inject workdir: %w", err)
	}
	defer os.RemoveAll(workDir)

	initPath := filepath.Join(workDir, "novitabox-init")
	if err := os.WriteFile(initPath, []byte(templateBoxdInitScript(s.cfg.Template.BoxdGuestPath, s.cfg.Template.BoxdGuestAddr)), 0o755); err != nil {
		return fmt.Errorf("write template init script: %w", err)
	}

	if err := runDebugfs(ctx, rootfsPath, "mkdir /novitabox"); err != nil && !strings.Contains(err.Error(), "File exists") {
		return err
	}
	if err := runDebugfs(ctx, rootfsPath, "rm /novitabox/init"); err != nil && !strings.Contains(err.Error(), "File not found") {
		return err
	}
	if err := runDebugfsScript(ctx, rootfsPath, []string{
		"cd /novitabox",
		"write " + debugfsQuote(initPath) + " init",
		"sif init mode 0100755",
	}); err != nil {
		return err
	}
	if err := runDebugfs(ctx, rootfsPath, "stat /novitabox/init"); err != nil {
		return err
	}

	return nil
}

func templateBoxdInitScript(boxdPath string, listenAddr string) string {
	return `#!/bin/sh
mount -t proc proc /proc 2>/dev/null || true
mount -t sysfs sysfs /sys 2>/dev/null || true
mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
mkdir -p /dev/pts 2>/dev/null || true
mount -t devpts devpts /dev/pts 2>/dev/null || true
mkdir -p /novitabox/agent 2>/dev/null || true
mount -o ro /dev/vdb /novitabox/agent 2>/dev/null || true
exec ` + boxdPath + ` --addr ` + listenAddr + `
`
}

func runDebugfs(ctx context.Context, rootfsPath string, command string) error {
	cmd := exec.CommandContext(ctx, "debugfs", "-w", "-R", command, rootfsPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("debugfs %q failed: %w: %s", command, err, strings.TrimSpace(string(output)))
	}
	if hasDebugfsCommandError(output) {
		return fmt.Errorf("debugfs %q failed: %s", command, strings.TrimSpace(string(output)))
	}
	return nil
}

func runDebugfsScript(ctx context.Context, rootfsPath string, commands []string) error {
	workDir := filepath.Dir(rootfsPath)
	cmdFile, err := os.CreateTemp(workDir, ".debugfs-commands-*")
	if err != nil {
		return fmt.Errorf("create debugfs command file: %w", err)
	}
	cmdFilePath := cmdFile.Name()
	defer os.Remove(cmdFilePath)

	for _, command := range commands {
		if _, err := fmt.Fprintln(cmdFile, command); err != nil {
			_ = cmdFile.Close()
			return fmt.Errorf("write debugfs command file: %w", err)
		}
	}
	if err := cmdFile.Close(); err != nil {
		return fmt.Errorf("close debugfs command file: %w", err)
	}

	cmd := exec.CommandContext(ctx, "debugfs", "-w", "-f", cmdFilePath, rootfsPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("debugfs script failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if hasDebugfsCommandError(output) {
		return fmt.Errorf("debugfs script failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func hasDebugfsCommandError(output []byte) bool {
	text := string(output)
	return strings.Contains(text, "File not found") ||
		strings.Contains(text, "Ext2 directory already exists") ||
		strings.Contains(text, "while creating directory")
}

func debugfsQuote(path string) string {
	if !strings.ContainsAny(path, " \t\n\"'") {
		return path
	}
	return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"`
}

func (s *artifactService) templateArtifactPaths(ctx context.Context, templateID string, templateDir string, runtimeDriver string) (artifactPaths, error) {
	if s.store != nil {
		record, err := s.store.GetTemplate(ctx, templateID)
		if err == nil && record.RootfsPath != "" {
			return artifactPaths{
				RootfsPath:   record.RootfsPath,
				MemfilePath:  record.MemfilePath,
				SnapfilePath: record.SnapfilePath,
			}, nil
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return artifactPaths{}, err
		}
	}
	return templateArtifactPaths(templateDir, runtimeDriver), nil
}

func (s *artifactService) injectTemplateRuntimeFiles(ctx context.Context, paths artifactPaths, runtimeDriver string) error {
	if isGVisorRuntimeDriver(runtimeDriver) {
		return s.injectTemplateBoxdBinary(paths.RootfsPath)
	}
	return s.injectTemplateInit(ctx, paths.RootfsPath)
}

func (s *artifactService) injectTemplateBoxdBinary(rootfsPath string) error {
	return injectGVisorBoxdBinary(s.cfg, rootfsPath)
}

func injectGVisorBoxdBinary(cfg config.Config, rootfsPath string) error {
	guestPath := cfg.Template.BoxdGuestPath
	if guestPath == "" {
		guestPath = "/novitabox/agent/boxd"
	}
	boxdPath := cfg.Template.BoxdBinaryPath
	if boxdPath == "" {
		return errors.New("template boxd binary path is required")
	}
	if err := os.MkdirAll(filepath.Dir(shimruntime.ContainerPathInRootfs(rootfsPath, guestPath)), 0o755); err != nil {
		return fmt.Errorf("create gvisor boxd dir: %w", err)
	}
	if err := cloneOrCopyFile(boxdPath, shimruntime.ContainerPathInRootfs(rootfsPath, guestPath)); err != nil {
		return fmt.Errorf("inject gvisor boxd binary: %w", err)
	}
	if err := injectTemplateNetworkingFiles(rootfsPath, true); err != nil {
		return err
	}
	if err := injectDomesticAptSources(rootfsPath); err != nil {
		return err
	}
	return nil
}

func injectTemplateNetworkingFiles(rootfsPath string, preserveHostResolver bool) error {
	if err := writeTemplateResolvConf(filepath.Join(rootfsPath, "etc", "resolv.conf"), preserveHostResolver); err != nil {
		return fmt.Errorf("inject gvisor resolv.conf: %w", err)
	}
	if err := writeTemplateHosts(filepath.Join(rootfsPath, "etc", "hosts")); err != nil {
		return fmt.Errorf("inject gvisor hosts: %w", err)
	}
	return nil
}

func writeTemplateResolvConf(path string, preserveHostResolver bool) error {
	_ = preserveHostResolver
	nameservers := []string{"114.114.114.114"}
	var buf bytes.Buffer
	for _, ns := range nameservers {
		buf.WriteString("nameserver ")
		buf.WriteString(ns)
		buf.WriteByte('\n')
	}
	buf.WriteString("options timeout:2 attempts:2\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

func injectDomesticAptSources(rootfsPath string) error {
	aptDir := filepath.Join(rootfsPath, "etc", "apt")
	paths := []string{filepath.Join(aptDir, "sources.list")}
	if entries, err := os.ReadDir(filepath.Join(aptDir, "sources.list.d")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".list") || strings.HasSuffix(name, ".sources") {
				paths = append(paths, filepath.Join(aptDir, "sources.list.d", name))
			}
		}
	}

	changed := false
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read apt sources %q: %w", path, err)
		}
		rewritten := rewriteAptSources(data)
		if bytes.Equal(rewritten, data) {
			continue
		}
		if err := os.WriteFile(path, rewritten, 0o644); err != nil {
			return fmt.Errorf("write apt sources %q: %w", path, err)
		}
		changed = true
	}
	if changed {
		return nil
	}
	return nil
}

func rewriteAptSources(data []byte) []byte {
	replacements := []struct {
		old string
		new string
	}{
		{"https://archive.ubuntu.com/ubuntu", "https://mirrors.aliyun.com/ubuntu"},
		{"https://archive.ubuntu.com/ubuntu/", "https://mirrors.aliyun.com/ubuntu/"},
		{"http://archive.ubuntu.com/ubuntu", "http://mirrors.aliyun.com/ubuntu"},
		{"http://archive.ubuntu.com/ubuntu/", "http://mirrors.aliyun.com/ubuntu/"},
		{"https://security.ubuntu.com/ubuntu", "https://mirrors.aliyun.com/ubuntu"},
		{"https://security.ubuntu.com/ubuntu/", "https://mirrors.aliyun.com/ubuntu/"},
		{"http://security.ubuntu.com/ubuntu", "http://mirrors.aliyun.com/ubuntu"},
		{"http://security.ubuntu.com/ubuntu/", "http://mirrors.aliyun.com/ubuntu/"},
		{"https://ports.ubuntu.com/ubuntu-ports", "https://mirrors.aliyun.com/ubuntu-ports"},
		{"https://ports.ubuntu.com/ubuntu-ports/", "https://mirrors.aliyun.com/ubuntu-ports/"},
		{"http://ports.ubuntu.com/ubuntu-ports", "http://mirrors.aliyun.com/ubuntu-ports"},
		{"http://ports.ubuntu.com/ubuntu-ports/", "http://mirrors.aliyun.com/ubuntu-ports/"},
		{"https://deb.debian.org/debian", "https://mirrors.aliyun.com/debian"},
		{"https://deb.debian.org/debian/", "https://mirrors.aliyun.com/debian/"},
		{"http://deb.debian.org/debian", "http://mirrors.aliyun.com/debian"},
		{"http://deb.debian.org/debian/", "http://mirrors.aliyun.com/debian/"},
		{"https://security.debian.org/debian-security", "https://mirrors.aliyun.com/debian-security"},
		{"https://security.debian.org/debian-security/", "https://mirrors.aliyun.com/debian-security/"},
		{"http://security.debian.org/debian-security", "http://mirrors.aliyun.com/debian-security"},
		{"http://security.debian.org/debian-security/", "http://mirrors.aliyun.com/debian-security/"},
		{"https://ftp.debian.org/debian", "https://mirrors.aliyun.com/debian"},
		{"https://ftp.debian.org/debian/", "https://mirrors.aliyun.com/debian/"},
		{"http://ftp.debian.org/debian", "http://mirrors.aliyun.com/debian"},
		{"http://ftp.debian.org/debian/", "http://mirrors.aliyun.com/debian/"},
	}
	out := string(data)
	for _, replacement := range replacements {
		out = strings.ReplaceAll(out, replacement.old, replacement.new)
	}
	return []byte(out)
}

func hasUsableNameserver(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "nameserver ") {
			continue
		}
		ns := strings.TrimSpace(strings.TrimPrefix(line, "nameserver "))
		if ns == "" || strings.HasPrefix(ns, "127.") || ns == "::1" || ns == "localhost" {
			continue
		}
		return true
	}
	return false
}

func writeTemplateHosts(path string) error {
	content := templateHostsContent()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

func templateHostsContent() string {
	return templateHostsContentWithResolver(resolveTemplateHostIPv4)
}

func templateHostsContentWithResolver(resolve func(string) string) string {
	var buf strings.Builder
	buf.WriteString("127.0.0.1 localhost\n")
	buf.WriteString("::1 localhost ip6-localhost ip6-loopback\n")
	for _, entry := range templateHostMappingsWithResolver(resolve) {
		buf.WriteString(entry.ip)
		buf.WriteByte(' ')
		buf.WriteString(entry.host)
		buf.WriteByte('\n')
	}
	return buf.String()
}

type templateHostMapping struct {
	host string
	ip   string
}

func templateHostMappings() []templateHostMapping {
	return templateHostMappingsWithResolver(resolveTemplateHostIPv4)
}

func templateHostMappingsWithResolver(resolve func(string) string) []templateHostMapping {
	hosts := []string{
		"mirrors.aliyun.com",
		"archive.ubuntu.com",
		"security.ubuntu.com",
	}
	out := make([]templateHostMapping, 0, len(hosts))
	for _, host := range hosts {
		ip := resolve(host)
		if ip == "" {
			continue
		}
		out = append(out, templateHostMapping{host: host, ip: ip})
	}
	return out
}

func resolveTemplateHostIPv4(host string) string {
	ips, err := net.LookupIP(host)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String()
		}
	}
	return ""
}

func collectNameservers() []string {
	candidates := []string{"/etc/resolv.conf", "/run/systemd/resolve/resolv.conf"}
	seen := map[string]struct{}{}
	var out []string
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "nameserver ") {
				continue
			}
			ns := strings.TrimSpace(strings.TrimPrefix(line, "nameserver "))
			if ns == "" || strings.HasPrefix(ns, "127.") || ns == "::1" || ns == "localhost" {
				continue
			}
			if _, ok := seen[ns]; ok {
				continue
			}
			seen[ns] = struct{}{}
			out = append(out, ns)
		}
		if len(out) > 0 {
			return out
		}
	}
	return out
}

func (s *artifactService) createGVisorTemplate(ctx context.Context, req *novitaboxv1.CreateTemplateRequest, templateID string, paths artifactPaths, runtimeDriver string) error {
	buildID, err := newInternalBuildID()
	if err != nil {
		return fmt.Errorf("generate template build id: %w", err)
	}
	buildDir := filepath.Join(layout.New(s.cfg.RootDir).SandboxDir(buildID))
	var internalNetworkSlot uint32
	success := false
	if err := os.RemoveAll(buildDir); err != nil {
		return fmt.Errorf("remove stale template build sandbox: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create template build dir: %w", err)
	}
	defer func() {
		if success {
			if err := cleanupInternalSandbox(context.Background(), s.cfg, buildID, internalNetworkSlot, runtimeTypeFromRuntimeDriver(runtimeDriver)); err != nil {
				s.logger.Warn("cleanup template build sandbox failed", "sandbox_id", buildID, "error", err)
			}
			return
		}
		if keepFailedGVisorTemplateBuilds() {
			s.logger.Warn("preserving failed gvisor template build sandbox for debugging", "sandbox_id", buildID)
			return
		}
		if err := cleanupInternalSandbox(context.Background(), s.cfg, buildID, internalNetworkSlot, runtimeTypeFromRuntimeDriver(runtimeDriver)); err != nil {
			s.logger.Warn("cleanup failed template build sandbox failed", "sandbox_id", buildID, "error", err)
		}
	}()

	shimSocket := filepath.Join(buildDir, "shim.sock")
	if err := ensureShim(ctx, s.cfg, shimSocket, runtimeDriver); err != nil {
		return fmt.Errorf("start template build shim: %w", err)
	}

	shim, closeShim, err := dialShim(ctx, shimSocket)
	if err != nil {
		return fmt.Errorf("dial template build shim: %w", err)
	}
	defer closeShim()

	spec := &novitaboxv1.RuntimeSpec{
		SandboxId:   buildID,
		RuntimeType: runtimeTypeFromRuntimeDriver(runtimeDriver),
		Machine: &novitaboxv1.MachineSpec{
			Vcpu:     s.cfg.Template.VCPU,
			MemoryMb: s.cfg.Template.MemoryMB,
		},
		Rootfs: &novitaboxv1.RootfsSpec{
			Path:   paths.RootfsPath,
			Format: "dir",
		},
		Agent: &novitaboxv1.AgentSpec{
			Type:     "boxd",
			Protocol: "grpc",
			Port:     49983,
		},
	}
	if err := s.attachAgentDrive(ctx, spec); err != nil {
		return err
	}

	networkSpec, err := s.internalSandboxNetworkSpecForTemplateBuild(ctx, buildID)
	if err != nil {
		return fmt.Errorf("prepare template build network spec: %w", err)
	}
	if networkSpec != nil {
		internalNetworkSlot = networkSpec.GetSlot()
	}
	spec.Network = networkSpec
	if err := newSandboxNetworkManager(s.cfg).Prepare(ctx, spec.GetRuntimeType(), spec.GetNetwork()); err != nil {
		return fmt.Errorf("prepare template build network: %w", err)
	}

	if _, err := shim.CreateRuntime(ctx, &novitaboxv1.CreateRuntimeRequest{RuntimeSpec: spec}); err != nil {
		return fmt.Errorf("create template build runtime: %w", err)
	}
	if holdGVisorTemplateBuildSandbox() {
		return fmt.Errorf("holding gvisor template build sandbox for debugging")
	}

	agentURL, err := templateBuildAgentURL(s.cfg, buildID, spec.GetNetwork(), runtimeDriver)
	if err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return err
	}
	if err := s.waitTemplateRuntimeReady(ctx, agentURL.health); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return err
	}
	if err := s.runTemplateBuildCommands(ctx, req, agentURL.exec); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return err
	}
	if _, err := shim.StopRuntime(ctx, &novitaboxv1.StopRuntimeRequest{SandboxId: buildID, TimeoutSeconds: 30}); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return fmt.Errorf("stop gvisor template build runtime: %w", err)
	}
	success = true
	return nil
}

func (s *artifactService) createTemplateSnapshot(ctx context.Context, req *novitaboxv1.CreateTemplateRequest, templateID string, paths artifactPaths, runtimeDriver string) error {
	if s.cfg.Template.KernelPath == "" {
		return errors.New("template snapshot build requires --template-kernel")
	}
	if strings.EqualFold(runtimeDriver, "stub") {
		if err := ensureFile(paths.MemfilePath); err != nil {
			return fmt.Errorf("create template memfile placeholder: %w", err)
		}
		if err := ensureFile(paths.SnapfilePath); err != nil {
			return fmt.Errorf("create template snapfile placeholder: %w", err)
		}
		return nil
	}

	buildID, err := newInternalBuildID()
	if err != nil {
		return fmt.Errorf("generate template build id: %w", err)
	}
	buildDir := filepath.Join(layout.New(s.cfg.RootDir).SandboxDir(buildID))
	var internalNetworkSlot uint32
	success := false
	if err := os.RemoveAll(buildDir); err != nil {
		return fmt.Errorf("remove stale template build sandbox: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create template snapshot build dir: %w", err)
	}
	defer func() {
		if success {
			if err := cleanupInternalSandbox(context.Background(), s.cfg, buildID, internalNetworkSlot, runtimeTypeFromRuntimeDriver(runtimeDriver)); err != nil {
				s.logger.Warn("cleanup template build sandbox failed", "sandbox_id", buildID, "error", err)
			}
			return
		}
		if err := cleanupInternalSandbox(context.Background(), s.cfg, buildID, internalNetworkSlot, runtimeTypeFromRuntimeDriver(runtimeDriver)); err != nil {
			s.logger.Warn("cleanup failed template build sandbox failed", "sandbox_id", buildID, "error", err)
		}
	}()

	shimSocket := filepath.Join(buildDir, "shim.sock")
	if err := ensureShim(ctx, s.cfg, shimSocket, runtimeDriver); err != nil {
		return fmt.Errorf("start template build shim: %w", err)
	}

	shim, closeShim, err := dialShim(ctx, shimSocket)
	if err != nil {
		return fmt.Errorf("dial template build shim: %w", err)
	}
	defer closeShim()

	spec := &novitaboxv1.RuntimeSpec{
		SandboxId:   buildID,
		RuntimeType: runtimeTypeFromRuntimeDriver(runtimeDriver),
		Machine: &novitaboxv1.MachineSpec{
			Vcpu:     s.cfg.Template.VCPU,
			MemoryMb: s.cfg.Template.MemoryMB,
		},
		Kernel: &novitaboxv1.KernelSpec{
			KernelPath: s.cfg.Template.KernelPath,
			KernelArgs: templateKernelArgs(s.cfg.Template.KernelArgs),
		},
		Rootfs: &novitaboxv1.RootfsSpec{
			Path:   paths.RootfsPath,
			Format: "ext4",
		},
		Snapshot: &novitaboxv1.SnapshotSpec{
			MemfilePath:  paths.MemfilePath,
			SnapfilePath: paths.SnapfilePath,
			SnapshotType: "full",
		},
	}
	networkSpec, err := s.internalSandboxNetworkSpecForTemplateBuild(ctx, buildID)
	if err != nil {
		return fmt.Errorf("prepare template build network spec: %w", err)
	}
	if networkSpec != nil {
		internalNetworkSlot = networkSpec.GetSlot()
	}
	spec.Network = networkSpec
	if err := s.attachAgentDrive(ctx, spec); err != nil {
		return err
	}
	if err := newSandboxNetworkManager(s.cfg).Prepare(ctx, spec.GetRuntimeType(), spec.GetNetwork()); err != nil {
		return fmt.Errorf("prepare template build network: %w", err)
	}

	if _, err := shim.CreateRuntime(ctx, &novitaboxv1.CreateRuntimeRequest{RuntimeSpec: spec}); err != nil {
		return fmt.Errorf("create template build runtime: %w", err)
	}
	agentURL, err := templateBuildAgentURL(s.cfg, buildID, spec.GetNetwork(), runtimeDriver)
	if err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return err
	}
	if err := s.waitTemplateRuntimeReady(ctx, agentURL.health); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return err
	}
	if err := s.runTemplateBuildCommands(ctx, req, agentURL.exec); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return err
	}

	if _, err := shim.PauseRuntime(ctx, &novitaboxv1.PauseRuntimeRequest{SandboxId: buildID}); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return fmt.Errorf("export firecracker template snapshot: %w", err)
	}

	success = true
	return nil
}

func (s *artifactService) internalSandboxNetworkSpec(ctx context.Context, sandboxID string) (*novitaboxv1.NetworkSpec, error) {
	manager := newSandboxNetworkManager(s.cfg)
	if !s.cfg.Network.Enabled {
		return nil, nil
	}
	maxSlot, err := manager.MaxSlots()
	if err != nil {
		return nil, err
	}
	used := map[uint32]struct{}{}
	if s.store != nil {
		records, err := s.store.ListSandboxes(ctx)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if record.NetworkSlot > 0 {
				used[record.NetworkSlot] = struct{}{}
			}
		}
	}
	markUsedNetworkSlotsFromHostRoutes(ctx, s.cfg, used)
	for slot := uint32(1); slot <= maxSlot; slot++ {
		if _, ok := used[slot]; !ok {
			return manager.SpecForSlot(sandboxID, slot)
		}
	}
	return nil, fmt.Errorf("no internal sandbox network slots available")
}

func (s *artifactService) internalSandboxNetworkSpecForTemplateBuild(ctx context.Context, sandboxID string) (*novitaboxv1.NetworkSpec, error) {
	cfg := s.cfg
	cfg.Network.Enabled = true
	manager := newSandboxNetworkManager(cfg)
	maxSlot, err := manager.MaxSlots()
	if err != nil {
		return nil, err
	}
	used := map[uint32]struct{}{}
	if s.store != nil {
		records, err := s.store.ListSandboxes(ctx)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if record.NetworkSlot > 0 {
				used[record.NetworkSlot] = struct{}{}
			}
		}
	}
	markUsedNetworkSlotsFromHostRoutes(ctx, cfg, used)
	for slot := uint32(1); slot <= maxSlot; slot++ {
		if _, ok := used[slot]; !ok {
			return manager.SpecForSlot(sandboxID, slot)
		}
	}
	return nil, fmt.Errorf("no internal sandbox network slots available")
}

func markUsedNetworkSlotsFromHostRoutes(ctx context.Context, cfg config.Config, used map[uint32]struct{}) {
	out, err := exec.CommandContext(ctx, "ip", "route", "show").Output()
	if err != nil {
		return
	}
	markUsedNetworkSlotsFromRouteOutput(cfg.Network.HostAccessCIDR, string(out), used)
}

func markUsedNetworkSlotsFromRouteOutput(hostAccessCIDR string, routeOutput string, used map[uint32]struct{}) {
	for _, line := range strings.Split(routeOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		hostAccessIP := strings.TrimSuffix(fields[0], "/32")
		slot, err := networkSlotFromHostAccessIP(hostAccessCIDR, hostAccessIP)
		if err != nil {
			continue
		}
		used[slot] = struct{}{}
	}
}

func templateKernelArgs(configured []string) []string {
	if len(configured) > 0 {
		return configured
	}
	return []string{
		"console=ttyS0",
		"reboot=k",
		"panic=1",
		"pci=off",
		"8250.nr_uarts=1",
		"root=/dev/vda",
		"rw",
		"loglevel=7",
		"init=/novitabox/init",
	}
}

type templateBuildAgentURLs struct {
	health string
	exec   string
}

const templateBuildExecTimeout = 10 * time.Minute

func templateBuildAgentURL(cfg config.Config, buildID string, networkSpec *novitaboxv1.NetworkSpec, runtimeDriver string) (templateBuildAgentURLs, error) {
	if cfg.Template.AgentHealthURL != "" || cfg.Template.AgentExecURL != "" {
		return templateBuildAgentURLs{
			health: cfg.Template.AgentHealthURL,
			exec:   cfg.Template.AgentExecURL,
		}, nil
	}
	_, port, err := net.SplitHostPort(cfg.Boxd.Addr)
	if err != nil {
		return templateBuildAgentURLs{}, fmt.Errorf("parse boxd address %q: %w", cfg.Boxd.Addr, err)
	}
	if networkSpec == nil || networkSpec.GetHostAccessIp() == "" {
		baseURL := "http://127.0.0.1:" + port
		return templateBuildAgentURLs{
			health: baseURL + "/healthz",
			exec:   baseURL + "/exec",
		}, nil
	}
	hostIP, err := templateBuildAgentHostIP(networkSpec)
	if err != nil {
		return templateBuildAgentURLs{}, err
	}
	baseURL := "http://" + net.JoinHostPort(hostIP, port)
	return templateBuildAgentURLs{
		health: baseURL + "/healthz",
		exec:   baseURL + "/exec",
	}, nil
}

func templateBuildAgentHostIP(networkSpec *novitaboxv1.NetworkSpec) (string, error) {
	if networkSpec == nil {
		return "", nil
	}
	return networkSpec.GetHostAccessIp(), nil
}

func (s *artifactService) waitTemplateRuntimeReady(ctx context.Context, healthURL string) error {
	if healthURL != "" {
		return waitHTTPHealth(ctx, healthURL, time.Duration(s.cfg.Template.AgentWaitSecs)*time.Second)
	}

	wait := time.Duration(s.cfg.Template.SnapshotWaitSecs) * time.Second
	if wait <= 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *artifactService) runTemplateBuildCommands(ctx context.Context, req *novitaboxv1.CreateTemplateRequest, execURL string) error {
	if req.GetStartCmd() == "" && req.GetReadyCmd() == "" && len(req.GetSteps()) == 0 {
		return nil
	}
	if execURL == "" {
		return errors.New("template build commands require --template-agent-exec")
	}

	if req.GetStartCmd() != "" {
		if err := execTemplateCommand(ctx, execURL, []string{"/bin/sh", "-c", req.GetStartCmd()}, nil); err != nil {
			return fmt.Errorf("run template start_cmd: %w", err)
		}
	}
	for i, step := range req.GetSteps() {
		if !isExecutableTemplateBuildStepType(step.GetType()) {
			return fmt.Errorf("unsupported template build step %d type %q", i, step.GetType())
		}
		if len(step.GetArgs()) == 0 {
			return fmt.Errorf("template build step %d args are required", i)
		}
		cmd := templateBuildStepCommand(step)
		if err := execTemplateCommand(ctx, execURL, cmd, step.GetEnvVars()); err != nil {
			return fmt.Errorf("run template build step %d: %w", i, err)
		}
	}
	if req.GetReadyCmd() != "" {
		if err := execTemplateCommand(ctx, execURL, []string{"/bin/sh", "-c", req.GetReadyCmd()}, nil); err != nil {
			return fmt.Errorf("run template ready_cmd: %w", err)
		}
	}

	return nil
}

func isExecutableTemplateBuildStepType(stepType string) bool {
	switch strings.ToLower(stepType) {
	case "exec", "run":
		return true
	default:
		return false
	}
}

func templateBuildStepCommand(step *novitaboxv1.TemplateBuildStep) []string {
	args := step.GetArgs()
	if strings.EqualFold(step.GetType(), "run") && len(args) == 1 {
		return []string{"/bin/sh", "-c", args[0]}
	}
	return args
}

func execTemplateCommand(ctx context.Context, url string, cmd []string, envVars map[string]string) error {
	return execTemplateCommandWithClient(ctx, url, cmd, envVars, &http.Client{Timeout: templateBuildExecTimeout})
}

func execTemplateCommandWithClient(ctx context.Context, url string, cmd []string, envVars map[string]string, client *http.Client) error {
	body := map[string]any{
		"cmd": cmd,
	}
	if len(envVars) > 0 {
		body["envVars"] = envVars
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal exec request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create exec request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	return fmt.Errorf("exec endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
}

func waitHTTPHealth(ctx context.Context, url string, timeout time.Duration) error {
	return waitHTTPHealthWithClient(ctx, url, timeout, &http.Client{Timeout: 2 * time.Second})
}

func waitHTTPHealthWithClient(ctx context.Context, url string, timeout time.Duration, client *http.Client) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("create health request: %w", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("health endpoint returned status %d", resp.StatusCode)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}

	return fmt.Errorf("wait for template agent health %q timed out: %w", url, lastErr)
}

func (s *artifactService) materializeTemplateRootfs(ctx context.Context, req *novitaboxv1.CreateTemplateRequest, dest string, runtimeDriver string) error {
	switch {
	case req.GetFromTemplate() != "":
		source, err := s.store.GetTemplate(ctx, req.GetFromTemplate())
		if err != nil {
			return fmt.Errorf("get source template %q: %w", req.GetFromTemplate(), err)
		}
		if isGVisorRuntimeDriver(runtimeDriver) {
			if info, statErr := os.Stat(source.RootfsPath); statErr != nil || !info.IsDir() {
				return fmt.Errorf("gvisor template build requires a directory rootfs source, got %q", source.RootfsPath)
			}
		}
		return cloneOrCopyPath(source.RootfsPath, dest)
	case req.GetImageId() != "":
		source, err := s.store.GetImage(ctx, req.GetImageId())
		if err != nil {
			return fmt.Errorf("get source image %q: %w", req.GetImageId(), err)
		}
		if isGVisorRuntimeDriver(runtimeDriver) {
			if info, statErr := os.Stat(source.RootfsPath); statErr != nil || !info.IsDir() {
				return fmt.Errorf("gvisor template build requires a directory rootfs source, got %q", source.RootfsPath)
			}
		}
		return cloneOrCopyPath(source.RootfsPath, dest)
	case req.GetDockerImage() != "":
		return s.materializeDockerImage(ctx, req.GetDockerImage(), dest, runtimeDriver)
	case req.GetSandboxId() != "":
		source := sandboxRuntimePaths(s.cfg.RootDir, req.GetSandboxId()).RootfsPath
		if isGVisorRuntimeDriver(runtimeDriver) {
			if info, statErr := os.Stat(source); statErr != nil || !info.IsDir() {
				return fmt.Errorf("gvisor template build requires a directory rootfs source, got %q", source)
			}
		}
		return cloneOrCopyPath(source, dest)
	default:
		return errors.New("one of from_template, image_id, docker_image, or sandbox_id is required")
	}
}

func (s *artifactService) materializeDockerImage(ctx context.Context, image string, dest string, runtimeDriver string) error {
	if isLocalFile(image) {
		return cloneOrCopyPath(image, dest)
	}

	s.logger.Info("exporting docker image to rootfs", "image", image, "dest", dest)
	workDir, err := os.MkdirTemp(filepath.Dir(dest), ".docker-rootfs-*")
	if err != nil {
		return fmt.Errorf("create docker export workdir: %w", err)
	}
	defer os.RemoveAll(workDir)

	containerName := "novitabox-build-" + strings.NewReplacer("/", "-", ":", "-").Replace(filepath.Base(dest)) + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
	create := exec.CommandContext(ctx, "docker", "create", "--name", containerName, image)
	if output, err := create.CombinedOutput(); err != nil {
		return fmt.Errorf("docker create %q failed: %w: %s", image, err, strings.TrimSpace(string(output)))
	}
	defer exec.CommandContext(context.Background(), "docker", "rm", "-f", containerName).Run()

	export := exec.CommandContext(ctx, "docker", "export", containerName)
	tarPath := filepath.Join(workDir, "rootfs.tar")
	tarFile, err := os.Create(tarPath)
	if err != nil {
		return fmt.Errorf("create docker export tar: %w", err)
	}
	export.Stdout = tarFile
	var exportErrBuf strings.Builder
	export.Stderr = &exportErrBuf
	exportErr := export.Run()
	closeErr := tarFile.Close()
	if exportErr != nil {
		return fmt.Errorf("docker export %q failed: %w: %s", image, exportErr, strings.TrimSpace(exportErrBuf.String()))
	}
	if closeErr != nil {
		return fmt.Errorf("close docker export tar: %w", closeErr)
	}

	rootfsDir := filepath.Join(workDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
		return fmt.Errorf("create docker rootfs dir: %w", err)
	}
	untar := exec.CommandContext(ctx, "tar", "-xf", tarPath, "-C", rootfsDir)
	if output, err := untar.CombinedOutput(); err != nil {
		return fmt.Errorf("extract docker rootfs failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	if isGVisorRuntimeDriver(runtimeDriver) {
		s.logger.Info("creating directory rootfs from docker export", "image", image, "dest", dest)
		if err := cloneOrCopyPath(rootfsDir, dest); err != nil {
			return err
		}
		return nil
	}
	s.logger.Info("creating ext4 rootfs from docker export", "image", image, "dest", dest)
	if err := createExt4FromDir(ctx, rootfsDir, dest); err != nil {
		return err
	}

	return nil
}

func (s *artifactService) ListTemplates(ctx context.Context, _ *novitaboxv1.ListTemplatesRequest) (*novitaboxv1.ListTemplatesResponse, error) {
	records, err := s.store.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}

	resp := &novitaboxv1.ListTemplatesResponse{
		Templates: make([]*novitaboxv1.TemplateInfo, 0, len(records)),
	}
	for _, record := range records {
		resp.Templates = append(resp.Templates, templateRecordToProto(record))
	}

	return resp, nil
}

func (s *artifactService) GetTemplate(ctx context.Context, req *novitaboxv1.GetTemplateRequest) (*novitaboxv1.TemplateInfo, error) {
	if req.GetTemplateId() == "" {
		return nil, errors.New("template_id is required")
	}

	record, err := s.store.GetTemplate(ctx, req.GetTemplateId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "template not found")
		}
		return nil, err
	}

	return templateRecordToProto(*record), nil
}

func (s *artifactService) DeleteTemplate(ctx context.Context, req *novitaboxv1.DeleteTemplateRequest) (*emptypb.Empty, error) {
	if req.GetTemplateId() == "" {
		return nil, errors.New("template_id is required")
	}

	record, err := s.store.GetTemplate(ctx, req.GetTemplateId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "template not found")
		}
		return nil, err
	}
	if err := s.store.DeleteTemplate(ctx, req.GetTemplateId()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "template not found")
		}
		return nil, err
	}
	if record.RootfsPath != "" {
		target := templateArtifactRemovalTarget(s.cfg.RootDir, req.GetTemplateId(), record.RootfsPath)
		if err := os.RemoveAll(target); err != nil {
			return nil, fmt.Errorf("remove template artifact directory: %w", err)
		}
	}

	return &emptypb.Empty{}, nil
}

func templateArtifactRemovalTarget(rootDir string, templateID string, rootfsPath string) string {
	templateDir := layout.New(rootDir).TemplateDir(templateID)
	cleanRootfs := filepath.Clean(rootfsPath)
	cleanTemplateDir := filepath.Clean(templateDir)
	if cleanRootfs == cleanTemplateDir || strings.HasPrefix(cleanRootfs, cleanTemplateDir+string(os.PathSeparator)) {
		return cleanTemplateDir
	}
	if info, err := os.Stat(cleanRootfs); err == nil && info.IsDir() {
		return cleanRootfs
	}
	return filepath.Dir(cleanRootfs)
}

func (s *artifactService) CreateImage(ctx context.Context, req *novitaboxv1.CreateImageRequest) (*novitaboxv1.ImageInfo, error) {
	imageID := strings.TrimSpace(req.GetImageId())
	if imageID == "" {
		generated, err := newImageID()
		if err != nil {
			return nil, fmt.Errorf("generate image id: %w", err)
		}
		imageID = generated
	}
	if _, err := s.store.GetImage(ctx, imageID); err == nil {
		return nil, status.Error(codes.AlreadyExists, "image already exists")
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	l := layout.New(s.cfg.RootDir)
	imageDir := l.ImageDir(imageID)
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return nil, fmt.Errorf("create image directory: %w", err)
	}

	template, err := s.store.GetTemplate(ctx, req.GetTemplateId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "template not found")
		}
		return nil, err
	}
	rootfsPath := filepath.Join(imageDir, "rootfs.ext4")
	if template.MemfilePath == "" || template.SnapfilePath == "" {
		rootfsPath = filepath.Join(imageDir, "rootfs")
	}
	if err := s.exportTemplateImageRootfs(ctx, *template, rootfsPath); err != nil {
		return nil, err
	}

	record := store.ImageRecord{
		ID:         imageID,
		RootfsPath: rootfsPath,
	}
	if err := s.store.CreateImage(ctx, record); err != nil {
		if isAlreadyExistsError(err) {
			return nil, status.Error(codes.AlreadyExists, "image already exists")
		}
		return nil, err
	}
	created, err := s.store.GetImage(ctx, imageID)
	if err != nil {
		return nil, err
	}

	return imageRecordToProto(*created), nil
}

func newImageID() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz1234567890"
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "img-" + string(b[:]), nil
}

func (s *artifactService) materializeImageRootfs(ctx context.Context, req *novitaboxv1.CreateImageRequest, dest string) error {
	if req.GetTemplateId() == "" {
		return status.Error(codes.InvalidArgument, "template_id is required")
	}
	record, err := s.store.GetTemplate(ctx, req.GetTemplateId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return status.Error(codes.NotFound, "template not found")
		}
		return err
	}
	return s.exportTemplateImageRootfs(ctx, *record, dest)
}

func (s *artifactService) exportTemplateImageRootfs(ctx context.Context, template store.TemplateRecord, dest string) error {
	if template.RootfsPath == "" {
		return status.Error(codes.InvalidArgument, "template rootfs is required")
	}
	if template.MemfilePath == "" || template.SnapfilePath == "" {
		return cloneOrCopyPath(template.RootfsPath, dest)
	}
	if strings.EqualFold(s.cfg.Boxshim.RuntimeDriver, "stub") {
		return cloneOrCopyPath(template.RootfsPath, dest)
	}

	buildID, err := newInternalBuildID()
	if err != nil {
		return fmt.Errorf("generate image build id: %w", err)
	}
	l := layout.New(s.cfg.RootDir)
	buildDir := l.SandboxDir(buildID)
	paths := sandboxRuntimePaths(s.cfg.RootDir, buildID)
	var internalNetworkSlot uint32
	success := false

	if err := os.RemoveAll(buildDir); err != nil {
		return fmt.Errorf("remove stale image build sandbox: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.RootfsPath), 0o755); err != nil {
		return fmt.Errorf("create image build snapshot directory: %w", err)
	}
	defer func() {
		if !success {
			return
		}
		if err := cleanupInternalSandbox(context.Background(), s.cfg, buildID, internalNetworkSlot, novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER); err != nil {
			s.logger.Warn("cleanup image build sandbox failed", "sandbox_id", buildID, "error", err)
		}
	}()

	if err := cloneOrCopyPath(template.MemfilePath, paths.MemfilePath); err != nil {
		return fmt.Errorf("prepare image build memfile from template %q: %w", template.ID, err)
	}
	if err := cloneOrCopyPath(template.SnapfilePath, paths.SnapfilePath); err != nil {
		return fmt.Errorf("prepare image build snapfile from template %q: %w", template.ID, err)
	}
	if s.cfg.Template.KernelPath == "" {
		return errors.New("image export from template requires --template-kernel")
	}
	if err := linkOrCopyFile(s.cfg.Template.KernelPath, paths.KernelPath); err != nil {
		return fmt.Errorf("prepare image build kernel: %w", err)
	}

	shimSocket := filepath.Join(buildDir, "shim.sock")
	if err := ensureShim(ctx, s.cfg, shimSocket, "firecracker"); err != nil {
		return fmt.Errorf("start image build shim: %w", err)
	}

	shim, closeShim, err := dialShim(ctx, shimSocket)
	if err != nil {
		return fmt.Errorf("dial image build shim: %w", err)
	}
	defer closeShim()

	networkSpec, err := s.internalSandboxNetworkSpec(ctx, buildID)
	if err != nil {
		return fmt.Errorf("prepare image build network spec: %w", err)
	}
	if networkSpec != nil {
		internalNetworkSlot = networkSpec.GetSlot()
	}
	spec := s.imageBuildRuntimeSpec(buildID, template.RootfsPath, paths, networkSpec)
	if err := s.attachAgentDrive(ctx, spec); err != nil {
		return err
	}
	if err := newSandboxNetworkManager(s.cfg).Prepare(ctx, spec.GetRuntimeType(), spec.GetNetwork()); err != nil {
		return fmt.Errorf("prepare image build network: %w", err)
	}

	return withWritableTemplateRootfs(template.RootfsPath, func(workRootfsPath string) error {
		if _, err := shim.ResumeRuntime(ctx, &novitaboxv1.ResumeRuntimeRequest{RuntimeSpec: spec}); err != nil {
			return fmt.Errorf("resume template runtime for image export: %w", err)
		}
		if _, err := shim.StopRuntime(ctx, &novitaboxv1.StopRuntimeRequest{
			SandboxId:      buildID,
			TimeoutSeconds: 30,
		}); err != nil {
			return fmt.Errorf("stop image build runtime: %w", err)
		}
		if err := cloneOrCopyPath(workRootfsPath, dest); err != nil {
			return fmt.Errorf("export image rootfs from template %q: %w", template.ID, err)
		}
		success = true
		return nil
	})
}

func (s *artifactService) imageBuildRuntimeSpec(sandboxID string, rootfsPath string, paths sandboxRuntimeArtifactPaths, networkSpec *novitaboxv1.NetworkSpec) *novitaboxv1.RuntimeSpec {
	spec := &novitaboxv1.RuntimeSpec{
		SandboxId:   sandboxID,
		RuntimeType: novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER,
		Machine: &novitaboxv1.MachineSpec{
			Vcpu:     s.cfg.Template.VCPU,
			MemoryMb: s.cfg.Template.MemoryMB,
		},
		Kernel: &novitaboxv1.KernelSpec{
			KernelPath: paths.KernelPath,
			KernelArgs: templateKernelArgs(s.cfg.Template.KernelArgs),
		},
		Rootfs: &novitaboxv1.RootfsSpec{
			Path:   rootfsPath,
			Format: "ext4",
		},
		Snapshot: &novitaboxv1.SnapshotSpec{
			MemfilePath:  paths.MemfilePath,
			SnapfilePath: paths.SnapfilePath,
			SnapshotType: "full",
		},
		Network: networkSpec,
		Agent: &novitaboxv1.AgentSpec{
			Type:     "boxd",
			Protocol: "grpc",
			Port:     49983,
		},
	}
	return spec
}

func (s *sandboxService) attachAgentDrive(ctx context.Context, spec *novitaboxv1.RuntimeSpec) error {
	return attachAgentDrive(ctx, s.cfg, spec)
}

func (s *artifactService) attachAgentDrive(ctx context.Context, spec *novitaboxv1.RuntimeSpec) error {
	return attachAgentDrive(ctx, s.cfg, spec)
}

func attachAgentDrive(ctx context.Context, cfg config.Config, spec *novitaboxv1.RuntimeSpec) error {
	if spec == nil || strings.EqualFold(cfg.Boxshim.RuntimeDriver, "stub") || spec.GetRuntimeType() == novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER {
		return nil
	}
	agentPath, err := ensureAgentImage(ctx, cfg)
	if err != nil {
		return fmt.Errorf("prepare agent image: %w", err)
	}
	spec.ExtraDrives = removeExtraDrive(spec.GetExtraDrives(), "agent")
	spec.ExtraDrives = append(spec.ExtraDrives, &novitaboxv1.DriveSpec{
		DriveId:  "agent",
		Path:     agentPath,
		Readonly: true,
		Format:   "ext4",
	})
	return nil
}

func removeExtraDrive(drives []*novitaboxv1.DriveSpec, driveID string) []*novitaboxv1.DriveSpec {
	out := make([]*novitaboxv1.DriveSpec, 0, len(drives))
	for _, drive := range drives {
		if drive == nil || drive.GetDriveId() == driveID {
			continue
		}
		out = append(out, drive)
	}
	return out
}

func ensureAgentImage(ctx context.Context, cfg config.Config) (string, error) {
	if cfg.Template.BoxdBinaryPath == "" {
		return "", errors.New("template boxd binary path is required")
	}
	boxdData, err := os.ReadFile(cfg.Template.BoxdBinaryPath)
	if err != nil {
		return "", fmt.Errorf("read boxd binary: %w", err)
	}
	sum := sha256.Sum256(boxdData)
	hash := hex.EncodeToString(sum[:8])
	agentDir := filepath.Join(cfg.RootDir, "agents")
	agentPath := filepath.Join(agentDir, "boxd-"+hash+".ext4")
	if _, err := os.Stat(agentPath); err == nil {
		return agentPath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat agent image: %w", err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return "", fmt.Errorf("create agent directory: %w", err)
	}

	workDir, err := os.MkdirTemp(agentDir, ".agent-*")
	if err != nil {
		return "", fmt.Errorf("create agent workdir: %w", err)
	}
	defer os.RemoveAll(workDir)

	if err := os.WriteFile(filepath.Join(workDir, "boxd"), boxdData, 0o755); err != nil {
		return "", fmt.Errorf("write agent boxd: %w", err)
	}
	tmpPath := agentPath + fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())
	if err := createExt4FromDirSized(ctx, workDir, tmpPath, "64M"); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("create agent image: %w", err)
	}
	if err := os.Rename(tmpPath, agentPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("install agent image: %w", err)
	}
	return agentPath, nil
}

func withWritableTemplateRootfs(rootfsPath string, fn func(string) error) error {
	backupPath := rootfsPath + fmt.Sprintf(".image-export-backup-%d-%d", os.Getpid(), time.Now().UnixNano())
	workPath := rootfsPath + fmt.Sprintf(".image-export-work-%d-%d", os.Getpid(), time.Now().UnixNano())
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale rootfs backup: %w", err)
	}
	if err := os.Remove(workPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale rootfs work copy: %w", err)
	}
	if err := cloneOrCopyFile(rootfsPath, workPath); err != nil {
		return fmt.Errorf("create writable template rootfs copy: %w", err)
	}

	restored := false
	defer func() {
		if restored {
			return
		}
		_ = os.Remove(rootfsPath)
		_ = os.Rename(backupPath, rootfsPath)
		_ = os.Remove(workPath)
	}()

	if err := os.Rename(rootfsPath, backupPath); err != nil {
		_ = os.Remove(workPath)
		return fmt.Errorf("backup template rootfs: %w", err)
	}
	if err := os.Rename(workPath, rootfsPath); err != nil {
		_ = os.Rename(backupPath, rootfsPath)
		return fmt.Errorf("activate writable template rootfs: %w", err)
	}

	runErr := fn(rootfsPath)
	if restoreErr := restoreTemplateRootfs(rootfsPath, backupPath); restoreErr != nil {
		if runErr != nil {
			return fmt.Errorf("%w; additionally failed to restore template rootfs: %v", runErr, restoreErr)
		}
		return restoreErr
	}
	restored = true
	if runErr != nil {
		return runErr
	}
	return nil
}

func restoreTemplateRootfs(rootfsPath string, backupPath string) error {
	if err := os.Remove(rootfsPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove writable template rootfs: %w", err)
	}
	if err := os.Rename(backupPath, rootfsPath); err != nil {
		return fmt.Errorf("restore template rootfs: %w", err)
	}
	return nil
}

func cleanupInternalSandbox(ctx context.Context, cfg config.Config, sandboxID string, networkSlot uint32, runtimeType novitaboxv1.RuntimeType) error {
	_ = runtimeType
	sandboxDir := layout.New(cfg.RootDir).SandboxDir(sandboxID)
	shimSocket := filepath.Join(sandboxDir, "shim.sock")
	if shim, closeShim, err := dialShim(ctx, shimSocket); err == nil {
		_, _ = shim.KillRuntime(ctx, &novitaboxv1.KillRuntimeRequest{SandboxId: sandboxID})
		_ = closeShim()
	}
	if err := terminateShimProcess(sandboxDir, 5*time.Second); err != nil {
		return err
	}
	if networkSlot > 0 {
		if err := newSandboxNetworkManager(cfg).Cleanup(ctx, sandboxID, networkSlot); err != nil {
			return err
		}
	}
	return removeSandboxDirectory(sandboxDir)
}

func removeSandboxDirectory(sandboxDir string) error {
	if err := shimruntime.UnmountUnder(sandboxDir); err != nil {
		return fmt.Errorf("unmount sandbox mounts: %w", err)
	}
	if err := os.RemoveAll(sandboxDir); err != nil {
		return err
	}
	return nil
}

func (s *artifactService) ListImages(ctx context.Context, _ *novitaboxv1.ListImagesRequest) (*novitaboxv1.ListImagesResponse, error) {
	records, err := s.store.ListImages(ctx)
	if err != nil {
		return nil, err
	}

	resp := &novitaboxv1.ListImagesResponse{
		Images: make([]*novitaboxv1.ImageInfo, 0, len(records)),
	}
	for _, record := range records {
		resp.Images = append(resp.Images, imageRecordToProto(record))
	}

	return resp, nil
}

func (s *artifactService) GetImage(ctx context.Context, req *novitaboxv1.GetImageRequest) (*novitaboxv1.ImageInfo, error) {
	if req.GetImageId() == "" {
		return nil, status.Error(codes.InvalidArgument, "image_id is required")
	}

	record, err := s.store.GetImage(ctx, req.GetImageId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "image not found")
		}
		return nil, err
	}

	return imageRecordToProto(*record), nil
}

func (s *artifactService) DeleteImage(ctx context.Context, req *novitaboxv1.DeleteImageRequest) (*emptypb.Empty, error) {
	if req.GetImageId() == "" {
		return nil, status.Error(codes.InvalidArgument, "image_id is required")
	}

	record, err := s.store.GetImage(ctx, req.GetImageId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "image not found")
		}
		return nil, err
	}
	if err := s.store.DeleteImage(ctx, req.GetImageId()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "image not found")
		}
		return nil, err
	}
	if shouldRemoveImageDir(s.cfg.RootDir, req.GetImageId(), record.RootfsPath) {
		if err := os.RemoveAll(layout.New(s.cfg.RootDir).ImageDir(req.GetImageId())); err != nil {
			return nil, fmt.Errorf("remove image artifact directory: %w", err)
		}
	}

	return &emptypb.Empty{}, nil
}

type artifactPaths struct {
	RootfsPath   string
	MemfilePath  string
	SnapfilePath string
}

func templateArtifactPaths(dir string, runtimeDriver string) artifactPaths {
	if isGVisorRuntimeDriver(runtimeDriver) {
		return artifactPaths{
			RootfsPath: filepath.Join(dir, "rootfs"),
		}
	}
	return artifactPaths{
		RootfsPath:   filepath.Join(dir, "rootfs.ext4"),
		MemfilePath:  filepath.Join(dir, "memfile"),
		SnapfilePath: filepath.Join(dir, "snapfile"),
	}
}

func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func copyFile(src string, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source %q: %w", src, err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %q: %w", src, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination dir: %w", err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open destination %q: %w", dst, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %q to %q: %w", src, dst, err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync destination %q: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close destination %q: %w", dst, err)
	}
	if err := os.Chmod(dst, portableFileMode(info.Mode())); err != nil {
		return fmt.Errorf("chmod destination %q: %w", dst, err)
	}

	return nil
}

func cloneOrCopyFile(src string, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source %q: %w", src, err)
	}
	if err := reflinkFile(src, dst); err == nil {
		if err := os.Chmod(dst, portableFileMode(info.Mode())); err != nil {
			return fmt.Errorf("chmod destination %q: %w", dst, err)
		}
		return nil
	}
	return copyFile(src, dst)
}

func cloneOrCopyPath(src string, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source %q: %w", src, err)
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return cloneOrCopyFile(src, dst)
}

func copyDir(src string, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source dir %q: %w", src, err)
	}
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("remove destination dir %q: %w", dst, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create destination dir %q: %w", dst, err)
	}
	if err := os.Chmod(dst, portableFileMode(srcInfo.Mode())); err != nil {
		return fmt.Errorf("chmod destination dir %q: %w", dst, err)
	}
	return filepath.Walk(src, func(current string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if current == src {
			return nil
		}
		rel, err := filepath.Rel(src, current)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(current)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if info.IsDir() {
			if err := os.MkdirAll(target, portableFileMode(info.Mode())); err != nil {
				return err
			}
			return os.Chmod(target, portableFileMode(info.Mode()))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return cloneOrCopyFile(current, target)
	})
}

func portableFileMode(mode os.FileMode) os.FileMode {
	return mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

func linkOrCopyFile(src string, dst string) error {
	if src == "" {
		return errors.New("source path is required")
	}
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination dir: %w", err)
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing destination %q: %w", dst, err)
	}
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	return cloneOrCopyFile(src, dst)
}

func isLocalFile(value string) bool {
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") {
		info, err := os.Stat(value)
		return err == nil && !info.IsDir()
	}
	return false
}

func createExt4FromDir(ctx context.Context, sourceDir string, dest string) error {
	return createExt4FromDirSized(ctx, sourceDir, dest, "2G")
}

func createExt4FromDirSized(ctx context.Context, sourceDir string, dest string, size string) error {
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove old rootfs %q: %w", dest, err)
	}

	cmd := exec.CommandContext(ctx, "mkfs.ext4", "-O", "^64bit,^metadata_csum", "-d", sourceDir, "-F", dest, size)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4 rootfs failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

func templateRecordToProto(record store.TemplateRecord) *novitaboxv1.TemplateInfo {
	return &novitaboxv1.TemplateInfo{
		TemplateId:    record.ID,
		RootfsPath:    record.RootfsPath,
		MemfilePath:   record.MemfilePath,
		SnapfilePath:  record.SnapfilePath,
		CreatedAtUnix: record.CreatedAt.Unix(),
	}
}

func isGVisorRuntimeDriver(runtimeDriver string) bool {
	switch strings.ToLower(strings.TrimSpace(runtimeDriver)) {
	case "gvisor", "container", "runtime_type_container":
		return true
	default:
		return false
	}
}

func keepFailedGVisorTemplateBuilds() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("NOVITABOX_KEEP_FAILED_GVISOR_TEMPLATE_BUILDS")))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func holdGVisorTemplateBuildSandbox() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("NOVITABOX_HOLD_GVISOR_TEMPLATE_BUILDS")))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func newInternalBuildID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func imageRecordToProto(record store.ImageRecord) *novitaboxv1.ImageInfo {
	return &novitaboxv1.ImageInfo{
		ImageId:       record.ID,
		RootfsPath:    record.RootfsPath,
		CreatedAtUnix: record.CreatedAt.Unix(),
	}
}

func shouldRemoveImageDir(rootDir string, imageID string, rootfsPath string) bool {
	imageDir := layout.New(rootDir).ImageDir(imageID)
	return rootfsPath == "" || filepath.Dir(rootfsPath) == imageDir
}

type nodeService struct {
	novitaboxv1.UnimplementedBoxletNodeServiceServer
	cfg config.Config
}

func newNodeService(cfg config.Config) *nodeService {
	return &nodeService{cfg: cfg}
}

func (s *nodeService) NodeStatus(context.Context, *novitaboxv1.NodeStatusRequest) (*novitaboxv1.NodeStatusInfo, error) {
	return &novitaboxv1.NodeStatusInfo{
		NodeId:  "local",
		RootDir: s.cfg.RootDir,
		Ready:   true,
		RuntimeTypes: []novitaboxv1.RuntimeType{
			novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER,
			novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER,
			novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR,
		},
	}, nil
}

func (s *nodeService) ListRuntimes(context.Context, *novitaboxv1.ListRuntimesRequest) (*novitaboxv1.ListRuntimesResponse, error) {
	return &novitaboxv1.ListRuntimesResponse{
		Runtimes: []*novitaboxv1.RuntimeSummary{
			{
				RuntimeType:  novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER,
				Capabilities: firecrackerCapabilities(),
			},
			{
				RuntimeType:  novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER,
				Capabilities: gvisorCapabilities(),
			},
			{
				RuntimeType:  novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR,
				Capabilities: cloudHypervisorCapabilities(),
			},
		},
	}, nil
}

func (s *nodeService) GetRuntimeCapabilities(_ context.Context, req *novitaboxv1.GetRuntimeCapabilitiesRequest) (*novitaboxv1.RuntimeCapabilities, error) {
	switch req.GetRuntimeType() {
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER:
		return firecrackerCapabilities(), nil
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR:
		return cloudHypervisorCapabilities(), nil
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER:
		return gvisorCapabilities(), nil
	default:
		return &novitaboxv1.RuntimeCapabilities{}, nil
	}
}

func firecrackerCapabilities() *novitaboxv1.RuntimeCapabilities {
	return &novitaboxv1.RuntimeCapabilities{
		StartFromImage:    true,
		StartFromTemplate: true,
		StartFromSnapshot: true,
		Pause:             true,
		Resume:            true,
		FullSnapshot:      true,
		DiffSnapshot:      false,
		Gpu:               false,
		Vsock:             true,
		TapNetwork:        true,
		GracefulShutdown:  true,
		SerialConsole:     true,
		Jailer:            true,
	}
}

func cloudHypervisorCapabilities() *novitaboxv1.RuntimeCapabilities {
	return &novitaboxv1.RuntimeCapabilities{
		StartFromImage:    true,
		StartFromTemplate: true,
		StartFromSnapshot: true,
		Pause:             true,
		Resume:            true,
		FullSnapshot:      true,
		DiffSnapshot:      false,
		Gpu:               true,
		Vsock:             true,
		TapNetwork:        true,
		HotplugDisk:       true,
		HotplugNetwork:    true,
		GracefulShutdown:  true,
		SerialConsole:     true,
		Jailer:            true,
	}
}

func gvisorCapabilities() *novitaboxv1.RuntimeCapabilities {
	return &novitaboxv1.RuntimeCapabilities{
		StartFromImage:    true,
		StartFromTemplate: true,
		StartFromSnapshot: false,
		Pause:             false,
		Resume:            false,
		FullSnapshot:      false,
		DiffSnapshot:      false,
		Gpu:               true,
		Vsock:             false,
		TapNetwork:        false,
		GracefulShutdown:  true,
		SerialConsole:     false,
		Jailer:            false,
	}
}
