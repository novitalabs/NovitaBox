package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
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
	cfg    config.Config
	logger *log.Logger
	store  store.Store
}

func newSandboxService(cfg config.Config, logger *log.Logger, store store.Store) *sandboxService {
	return &sandboxService{cfg: cfg, logger: logger, store: store}
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
	spec := s.completeRuntimeSpec(record, req.GetRuntimeSpec())
	if err := s.store.CreateSandbox(ctx, record); err != nil {
		if isAlreadyExistsError(err) {
			return nil, status.Error(codes.AlreadyExists, "sandbox already exists")
		}
		return nil, err
	}
	if err := s.prepareSandboxRuntimeFiles(ctx, record, spec); err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		return nil, err
	}
	if err := ensureSnapshotSpecDirs(spec.Snapshot); err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		return nil, err
	}
	if err := newSandboxNetworkManager(s.cfg).Ensure(ctx, spec.GetNetwork()); err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		return nil, err
	}

	shimSocket := filepath.Join(sandboxDir, "shim.sock")
	if err := ensureShim(ctx, s.cfg, shimSocket); err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		return nil, err
	}

	shim, closeShim, err := dialShim(ctx, shimSocket)
	if err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		return nil, err
	}
	defer closeShim()

	runtimeInfo, err := shim.CreateRuntime(ctx, &novitaboxv1.CreateRuntimeRequest{RuntimeSpec: spec})
	if err != nil {
		_ = s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateFailed, "create")
		return nil, fmt.Errorf("create runtime: %w", err)
	}

	if err := s.store.UpdateSandboxState(ctx, sandboxID, sandbox.StateCreating, sandbox.StateRunning, "create"); err != nil {
		return nil, err
	}

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

	shim, closeShim, err := s.dialSandboxShim(ctx, record.ID)
	if err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "resume")
		return nil, err
	}
	defer closeShim()

	spec := s.runtimeSpecForSandbox(*record)
	if err := newSandboxNetworkManager(s.cfg).Ensure(ctx, spec.GetNetwork()); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "resume")
		return nil, err
	}
	if _, err := shim.ResumeRuntime(ctx, &novitaboxv1.ResumeRuntimeRequest{RuntimeSpec: spec}); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "resume")
		return nil, fmt.Errorf("resume runtime: %w", err)
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
	if shim, closeShim, err := s.dialSandboxShim(ctx, record.ID); err == nil {
		_, _ = shim.KillRuntime(ctx, &novitaboxv1.KillRuntimeRequest{SandboxId: record.ID})
		_ = closeShim()
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
	if err := os.RemoveAll(layout.New(s.cfg.RootDir).SandboxDir(record.ID)); err != nil {
		return nil, fmt.Errorf("remove sandbox directory: %w", err)
	}
	if err := newSandboxNetworkManager(s.cfg).Cleanup(ctx, record.ID); err != nil {
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
	return s.runtimeAction(ctx, req.GetSandboxId(), sandbox.StateStopping, sandbox.StateStopped, "stop", func(shim novitaboxv1.BoxShimClient, sandboxID string) error {
		_, err := shim.StopRuntime(ctx, &novitaboxv1.StopRuntimeRequest{SandboxId: sandboxID, TimeoutSeconds: req.GetTimeoutSeconds()})
		return err
	})
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

	shim, closeShim, err := s.dialSandboxShim(ctx, record.ID)
	if err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
		return nil, err
	}
	defer closeShim()

	spec := s.runtimeSpecForSandbox(*record)
	if err := newSandboxNetworkManager(s.cfg).Ensure(ctx, spec.GetNetwork()); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
		return nil, err
	}
	if _, err := shim.StartRuntime(ctx, &novitaboxv1.StartRuntimeRequest{RuntimeSpec: spec}); err != nil {
		_ = s.setSandboxState(ctx, record.ID, sandbox.StateFailed, "start")
		return nil, fmt.Errorf("start runtime: %w", err)
	}
	if err := s.setSandboxState(ctx, record.ID, sandbox.StateRunning, "start"); err != nil {
		return nil, err
	}

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

func isAlreadyExistsError(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *sandboxService) runtimeSpecForSandbox(record store.SandboxRecord) *novitaboxv1.RuntimeSpec {
	paths := sandboxRuntimePaths(s.cfg.RootDir, record.ID)
	networkSpec, _ := newSandboxNetworkManager(s.cfg).Spec(record.ID)
	return &novitaboxv1.RuntimeSpec{
		SandboxId:   record.ID,
		RuntimeType: runtimeTypeFromRecord(record.RuntimeType),
		Machine: &novitaboxv1.MachineSpec{
			Vcpu:     1,
			MemoryMb: 512,
		},
		Kernel: &novitaboxv1.KernelSpec{
			KernelPath: paths.KernelPath,
			KernelArgs: s.cfg.Template.KernelArgs,
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
		Network: networkSpec,
	}
}

func (s *sandboxService) completeRuntimeSpec(record store.SandboxRecord, spec *novitaboxv1.RuntimeSpec) *novitaboxv1.RuntimeSpec {
	if spec == nil {
		spec = s.runtimeSpecForSandbox(record)
	}
	paths := sandboxRuntimePaths(s.cfg.RootDir, record.ID)
	spec.SandboxId = record.ID
	spec.RuntimeType = runtimeTypeFromRecord(record.RuntimeType)
	if spec.Machine == nil {
		spec.Machine = &novitaboxv1.MachineSpec{
			Vcpu:     s.cfg.Template.VCPU,
			MemoryMb: s.cfg.Template.MemoryMB,
		}
	}
	if spec.Kernel == nil {
		spec.Kernel = &novitaboxv1.KernelSpec{}
	}
	if spec.Kernel.KernelPath == "" {
		spec.Kernel.KernelPath = paths.KernelPath
	}
	if spec.Rootfs == nil {
		spec.Rootfs = &novitaboxv1.RootfsSpec{
			Path:   paths.RootfsPath,
			Format: "ext4",
		}
	}
	if spec.Rootfs.Path == "" {
		spec.Rootfs.Path = paths.RootfsPath
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
	if networkSpec, err := newSandboxNetworkManager(s.cfg).Complete(record.ID, spec.Network); err == nil {
		spec.Network = networkSpec
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
	if spec.GetKernel().GetKernelPath() != "" {
		if s.cfg.Template.KernelPath == "" {
			return errors.New("sandbox runtime requires --template-kernel")
		}
		if err := linkOrCopyFile(s.cfg.Template.KernelPath, spec.GetKernel().GetKernelPath()); err != nil {
			return fmt.Errorf("prepare sandbox kernel: %w", err)
		}
	}
	if record.TemplateID != "" {
		template, err := s.store.GetTemplate(ctx, record.TemplateID)
		if err != nil {
			return fmt.Errorf("get template %q: %w", record.TemplateID, err)
		}
		if err := cloneOrCopyFile(template.RootfsPath, spec.GetRootfs().GetPath()); err != nil {
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
		if err := cloneOrCopyFile(image.RootfsPath, spec.GetRootfs().GetPath()); err != nil {
			return fmt.Errorf("prepare sandbox rootfs from image %q: %w", record.ImageID, err)
		}
		return nil
	}

	return nil
}

func ensureShim(ctx context.Context, cfg config.Config, socketPath string) error {
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
	cmd := exec.Command(
		shimBin,
		"--root", cfg.RootDir,
		"--socket", socketPath,
		"--runtime-driver", cfg.Boxshim.RuntimeDriver,
		"--firecracker-bin", cfg.Firecracker.BinaryPath,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start boxshim: %w", err)
	}

	pidPath := filepath.Join(filepath.Dir(socketPath), "shim.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644); err != nil {
		return fmt.Errorf("write shim pid: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release boxshim process: %w", err)
	}

	if err := waitShimReady(ctx, socketPath, 5*time.Second); err != nil {
		return fmt.Errorf("wait for boxshim %q ready: %w", socketPath, err)
	}
	return nil
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
	return &novitaboxv1.SandboxInfo{
		SandboxId:     record.ID,
		State:         sandboxStateToProto(record.State),
		RuntimeType:   runtimeType,
		TemplateId:    record.TemplateID,
		ImageId:       record.ImageID,
		SnapshotId:    record.SnapshotID,
		CreatedAtUnix: record.CreatedAt.Unix(),
		UpdatedAtUnix: record.UpdatedAt.Unix(),
	}
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
	case "runtime_type_container", "container":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER
	case "runtime_type_firecracker", "firecracker", "":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER
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

func (s *artifactService) CreateTemplate(ctx context.Context, req *novitaboxv1.CreateTemplateRequest) (*novitaboxv1.TemplateInfo, error) {
	templateID := req.GetTemplateId()
	if templateID == "" {
		return nil, errors.New("template_id is required")
	}
	s.logger.Info("starting template artifact build",
		"template_id", templateID,
		"docker_image", req.GetDockerImage(),
		"from_template", req.GetFromTemplate(),
		"image_id", req.GetImageId(),
		"sandbox_id", req.GetSandboxId(),
		"snapshot_enabled", s.cfg.Template.SnapshotEnabled,
	)

	l := layout.New(s.cfg.RootDir)
	templateDir := l.TemplateDir(templateID)
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create template directory: %w", err)
	}

	paths := templateArtifactPaths(templateDir)
	s.logger.Info("materializing template rootfs", "template_id", templateID, "rootfs_path", paths.RootfsPath)
	if err := s.materializeTemplateRootfs(ctx, req, paths.RootfsPath); err != nil {
		s.logger.Error("materialize template rootfs failed", "template_id", templateID, "error", err)
		return nil, err
	}

	if s.cfg.Template.SnapshotEnabled {
		s.logger.Info("injecting boxd into template rootfs", "template_id", templateID, "boxd_bin", s.cfg.Template.BoxdBinaryPath)
		if err := s.injectTemplateBoxd(ctx, paths.RootfsPath); err != nil {
			s.logger.Error("inject boxd into template rootfs failed", "template_id", templateID, "error", err)
			return nil, err
		}
		s.logger.Info("creating template snapshot", "template_id", templateID, "memfile_path", paths.MemfilePath, "snapfile_path", paths.SnapfilePath)
		if err := s.createTemplateSnapshot(ctx, req, templateID, paths); err != nil {
			s.logger.Error("create template snapshot failed", "template_id", templateID, "error", err)
			return nil, err
		}
	} else {
		if err := ensureFile(paths.MemfilePath); err != nil {
			return nil, fmt.Errorf("create template memfile placeholder: %w", err)
		}
		if err := ensureFile(paths.SnapfilePath); err != nil {
			return nil, fmt.Errorf("create template snapfile placeholder: %w", err)
		}
	}

	record := store.TemplateRecord{
		ID:           templateID,
		RootfsPath:   paths.RootfsPath,
		MemfilePath:  paths.MemfilePath,
		SnapfilePath: paths.SnapfilePath,
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

func (s *artifactService) injectTemplateBoxd(ctx context.Context, rootfsPath string) error {
	if s.cfg.Template.BoxdBinaryPath == "" {
		return nil
	}
	if _, err := os.Stat(s.cfg.Template.BoxdBinaryPath); err != nil {
		return fmt.Errorf("stat template boxd binary: %w", err)
	}

	workDir, err := os.MkdirTemp(filepath.Dir(rootfsPath), ".boxd-inject-*")
	if err != nil {
		return fmt.Errorf("create boxd inject workdir: %w", err)
	}
	defer os.RemoveAll(workDir)

	initPath := filepath.Join(workDir, "novitabox-init")
	if err := os.WriteFile(initPath, []byte(templateBoxdInitScript(s.cfg.Template.BoxdGuestPath, s.cfg.Template.BoxdGuestAddr)), 0o755); err != nil {
		return fmt.Errorf("write template boxd init script: %w", err)
	}

	commands := []string{
		"mkdir /novitabox",
		"write " + debugfsQuote(s.cfg.Template.BoxdBinaryPath) + " " + debugfsQuote(s.cfg.Template.BoxdGuestPath),
		"write " + debugfsQuote(initPath) + " /novitabox/init",
		"sif " + debugfsQuote(s.cfg.Template.BoxdGuestPath) + " mode 0100755",
		"sif /novitabox/init mode 0100755",
	}
	for _, command := range commands {
		if err := runDebugfs(ctx, rootfsPath, command); err != nil {
			if strings.HasPrefix(command, "mkdir ") && strings.Contains(err.Error(), "File exists") {
				continue
			}
			return err
		}
	}

	return nil
}

func templateBoxdInitScript(boxdPath string, listenAddr string) string {
	return `#!/bin/sh
mount -t proc proc /proc 2>/dev/null || true
mount -t sysfs sysfs /sys 2>/dev/null || true
mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
exec ` + boxdPath + ` --addr ` + listenAddr + `
`
}

func runDebugfs(ctx context.Context, rootfsPath string, command string) error {
	cmd := exec.CommandContext(ctx, "debugfs", "-w", "-R", command, rootfsPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("debugfs %q failed: %w: %s", command, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func debugfsQuote(path string) string {
	if !strings.ContainsAny(path, " \t\n\"'") {
		return path
	}
	return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"`
}

func (s *artifactService) createTemplateSnapshot(ctx context.Context, req *novitaboxv1.CreateTemplateRequest, templateID string, paths artifactPaths) error {
	if s.cfg.Template.KernelPath == "" {
		return errors.New("template snapshot build requires --template-kernel")
	}

	buildID := "template-build-" + templateID
	buildDir := filepath.Join(layout.New(s.cfg.RootDir).SandboxDir(buildID))
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create template snapshot build dir: %w", err)
	}

	shimSocket := filepath.Join(buildDir, "shim.sock")
	shimCfg := s.cfg
	shimCfg.Boxshim.RuntimeDriver = "firecracker"
	if err := ensureShim(ctx, shimCfg, shimSocket); err != nil {
		return fmt.Errorf("start template build shim: %w", err)
	}

	shim, closeShim, err := dialShim(ctx, shimSocket)
	if err != nil {
		return fmt.Errorf("dial template build shim: %w", err)
	}
	defer closeShim()

	spec := &novitaboxv1.RuntimeSpec{
		SandboxId:   buildID,
		RuntimeType: novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER,
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

	if _, err := shim.CreateRuntime(ctx, &novitaboxv1.CreateRuntimeRequest{RuntimeSpec: spec}); err != nil {
		return fmt.Errorf("create template build runtime: %w", err)
	}

	if err := s.waitTemplateRuntimeReady(ctx); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return err
	}
	if err := s.runTemplateBuildCommands(ctx, req); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return err
	}

	if _, err := shim.PauseRuntime(ctx, &novitaboxv1.PauseRuntimeRequest{SandboxId: buildID}); err != nil {
		_, _ = shim.KillRuntime(context.Background(), &novitaboxv1.KillRuntimeRequest{SandboxId: buildID})
		return fmt.Errorf("export firecracker template snapshot: %w", err)
	}

	return nil
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

func (s *artifactService) waitTemplateRuntimeReady(ctx context.Context) error {
	if s.cfg.Template.AgentHealthURL != "" {
		return waitHTTPHealth(ctx, s.cfg.Template.AgentHealthURL, time.Duration(s.cfg.Template.AgentWaitSecs)*time.Second)
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

func (s *artifactService) runTemplateBuildCommands(ctx context.Context, req *novitaboxv1.CreateTemplateRequest) error {
	if req.GetStartCmd() == "" && req.GetReadyCmd() == "" && len(req.GetSteps()) == 0 {
		return nil
	}
	if s.cfg.Template.AgentExecURL == "" {
		return errors.New("template build commands require --template-agent-exec")
	}

	if req.GetStartCmd() != "" {
		if err := execTemplateCommand(ctx, s.cfg.Template.AgentExecURL, []string{"/bin/sh", "-c", req.GetStartCmd()}, nil); err != nil {
			return fmt.Errorf("run template start_cmd: %w", err)
		}
	}
	for i, step := range req.GetSteps() {
		if step.GetType() != "exec" {
			return fmt.Errorf("unsupported template build step %d type %q", i, step.GetType())
		}
		if len(step.GetArgs()) == 0 {
			return fmt.Errorf("template build step %d args are required", i)
		}
		if err := execTemplateCommand(ctx, s.cfg.Template.AgentExecURL, step.GetArgs(), step.GetEnvVars()); err != nil {
			return fmt.Errorf("run template build step %d: %w", i, err)
		}
	}
	if req.GetReadyCmd() != "" {
		if err := execTemplateCommand(ctx, s.cfg.Template.AgentExecURL, []string{"/bin/sh", "-c", req.GetReadyCmd()}, nil); err != nil {
			return fmt.Errorf("run template ready_cmd: %w", err)
		}
	}

	return nil
}

func execTemplateCommand(ctx context.Context, url string, cmd []string, envVars map[string]string) error {
	return execTemplateCommandWithClient(ctx, url, cmd, envVars, &http.Client{Timeout: 30 * time.Second})
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

func (s *artifactService) materializeTemplateRootfs(ctx context.Context, req *novitaboxv1.CreateTemplateRequest, dest string) error {
	switch {
	case req.GetFromTemplate() != "":
		source, err := s.store.GetTemplate(ctx, req.GetFromTemplate())
		if err != nil {
			return fmt.Errorf("get source template %q: %w", req.GetFromTemplate(), err)
		}
		return cloneOrCopyFile(source.RootfsPath, dest)
	case req.GetImageId() != "":
		source, err := s.store.GetImage(ctx, req.GetImageId())
		if err != nil {
			return fmt.Errorf("get source image %q: %w", req.GetImageId(), err)
		}
		return cloneOrCopyFile(source.RootfsPath, dest)
	case req.GetDockerImage() != "":
		return s.materializeDockerImage(ctx, req.GetDockerImage(), dest)
	case req.GetSandboxId() != "":
		source := sandboxRuntimePaths(s.cfg.RootDir, req.GetSandboxId()).RootfsPath
		return cloneOrCopyFile(source, dest)
	default:
		return errors.New("one of from_template, image_id, docker_image, or sandbox_id is required")
	}
}

func (s *artifactService) materializeDockerImage(ctx context.Context, image string, dest string) error {
	if isLocalFile(image) {
		return cloneOrCopyFile(image, dest)
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
		if err := os.RemoveAll(filepath.Dir(record.RootfsPath)); err != nil {
			return nil, fmt.Errorf("remove template artifact directory: %w", err)
		}
	}

	return &emptypb.Empty{}, nil
}

func (s *artifactService) CreateImage(ctx context.Context, req *novitaboxv1.CreateImageRequest) (*novitaboxv1.ImageInfo, error) {
	imageID := req.GetImageId()
	if imageID == "" {
		return nil, status.Error(codes.InvalidArgument, "image_id is required")
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

	rootfsPath := filepath.Join(imageDir, "rootfs.ext4")
	if err := s.materializeImageRootfs(ctx, req, rootfsPath); err != nil {
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

func (s *artifactService) materializeImageRootfs(ctx context.Context, req *novitaboxv1.CreateImageRequest, dest string) error {
	switch {
	case req.GetDockerImage() != "":
		return s.materializeDockerImage(ctx, req.GetDockerImage(), dest)
	case req.GetTemplateId() != "":
		record, err := s.store.GetTemplate(ctx, req.GetTemplateId())
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return status.Error(codes.NotFound, "template not found")
			}
			return err
		}
		return cloneOrCopyFile(record.RootfsPath, dest)
	case req.GetSandboxId() != "":
		if _, err := s.store.GetSandbox(ctx, req.GetSandboxId()); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return status.Error(codes.NotFound, "sandbox not found")
			}
			return err
		}
		return cloneOrCopyFile(sandboxRuntimePaths(s.cfg.RootDir, req.GetSandboxId()).RootfsPath, dest)
	default:
		return status.Error(codes.InvalidArgument, "one of template_id, sandbox_id, or docker_image is required")
	}
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

func templateArtifactPaths(dir string) artifactPaths {
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
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %q to %q: %w", src, dst, err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync destination %q: %w", dst, err)
	}

	return nil
}

func cloneOrCopyFile(src string, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	if err := reflinkFile(src, dst); err == nil {
		return nil
	}
	return copyFile(src, dst)
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
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove old rootfs %q: %w", dest, err)
	}

	cmd := exec.CommandContext(ctx, "mkfs.ext4", "-O", "^64bit,^metadata_csum", "-d", sourceDir, "-F", dest, "2G")
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
