package runtime

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
	"sync"
	"syscall"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
)

const (
	firecrackerAPITimeout      = 10 * time.Second
	firecrackerPostStartWindow = 1 * time.Second
)

type FirecrackerDriver struct {
	cfg    config.Config
	logger *log.Logger
	client *firecrackerClient

	mu      sync.Mutex
	cmd     *exec.Cmd
	waitCh  chan error
	exited  bool
	exitErr error
	info    *novitaboxv1.RuntimeInfo
	spec    *novitaboxv1.RuntimeSpec
	logPath string
}

func NewFirecrackerDriver(cfg config.Config, logger *log.Logger) *FirecrackerDriver {
	return &FirecrackerDriver{
		cfg:    cfg,
		logger: logger,
	}
}

func (d *FirecrackerDriver) Create(ctx context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}
	if err := validateFirecrackerSpec(spec); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cmd != nil && d.cmd.Process != nil {
		return nil, fmt.Errorf("runtime for sandbox %q is already created", d.info.GetSandboxId())
	}

	sandboxDir := layout.New(d.cfg.RootDir).SandboxDir(spec.GetSandboxId())
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sandbox directory: %w", err)
	}

	cmd, apiSocket, err := d.startFirecrackerLocked(spec)
	if err != nil {
		return nil, err
	}

	if err := d.waitForAPI(ctx, apiSocket); err != nil {
		_ = d.killLocked()
		return nil, d.withLogTail(err)
	}

	if err := d.configureMachine(ctx, spec); err != nil {
		_ = d.killLocked()
		return nil, d.withLogTail(err)
	}
	if err := d.waitPostStartAliveLocked(ctx, firecrackerPostStartWindow); err != nil {
		_ = d.killLocked()
		return nil, d.withLogTail(err)
	}

	info := &novitaboxv1.RuntimeInfo{
		SandboxId:         spec.GetSandboxId(),
		RuntimeType:       novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER,
		State:             novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING,
		Pid:               int64(cmd.Process.Pid),
		ShimSocketPath:    d.cfg.Boxshim.SocketPath,
		RuntimeSocketPath: apiSocket,
	}
	d.info = info
	d.spec = spec

	d.logger.Info("created firecracker runtime",
		"sandbox_id", spec.GetSandboxId(),
		"pid", cmd.Process.Pid,
		"api_socket", apiSocket,
	)

	return cloneRuntimeInfo(info), nil
}

func (d *FirecrackerDriver) Pause(ctx context.Context, sandboxID string) (*novitaboxv1.RuntimeInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkSandboxLocked(sandboxID); err != nil {
		return nil, err
	}
	if d.spec.GetSnapshot() == nil || d.spec.GetSnapshot().GetMemfilePath() == "" || d.spec.GetSnapshot().GetSnapfilePath() == "" {
		return nil, errors.New("runtime_spec.snapshot.memfile_path and snapfile_path are required for firecracker pause")
	}
	if err := d.checkProcessAliveLocked(); err != nil {
		return nil, err
	}

	if err := d.client.patch(ctx, "/vm", firecrackerVMStateRequest{State: "Paused"}); err != nil {
		return nil, d.withLogTail(fmt.Errorf("pause firecracker vm: %w", err))
	}

	paths, err := prepareAtomicSnapshotPaths(d.spec.GetSnapshot().GetMemfilePath(), d.spec.GetSnapshot().GetSnapfilePath())
	if err != nil {
		return nil, err
	}
	defer paths.cleanup()

	req := firecrackerSnapshotCreateRequest{
		SnapshotType: "Full",
		SnapshotPath: paths.tmpSnapfilePath,
		MemFilePath:  paths.tmpMemfilePath,
	}
	if err := d.client.put(ctx, "/snapshot/create", req); err != nil {
		return nil, d.withLogTail(fmt.Errorf("create firecracker snapshot: %w", err))
	}
	if err := paths.commit(); err != nil {
		return nil, fmt.Errorf("commit firecracker snapshot files: %w", err)
	}
	if err := d.killLocked(); err != nil {
		return nil, err
	}

	d.info.State = novitaboxv1.RuntimeState_RUNTIME_STATE_PAUSED
	d.info.Pid = 0
	d.info.ErrorMessage = ""
	return cloneRuntimeInfo(d.info), nil
}

type atomicSnapshotPaths struct {
	memfilePath     string
	snapfilePath    string
	tmpMemfilePath  string
	tmpSnapfilePath string
	oldMemfilePath  string
	oldSnapfilePath string
	committed       bool
}

func prepareAtomicSnapshotPaths(memfilePath string, snapfilePath string) (*atomicSnapshotPaths, error) {
	if err := os.MkdirAll(filepath.Dir(memfilePath), 0o755); err != nil {
		return nil, fmt.Errorf("create memfile directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(snapfilePath), 0o755); err != nil {
		return nil, fmt.Errorf("create snapfile directory: %w", err)
	}

	suffix := fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())
	paths := &atomicSnapshotPaths{
		memfilePath:     memfilePath,
		snapfilePath:    snapfilePath,
		tmpMemfilePath:  memfilePath + suffix,
		tmpSnapfilePath: snapfilePath + suffix,
		oldMemfilePath:  memfilePath + ".old" + suffix,
		oldSnapfilePath: snapfilePath + ".old" + suffix,
	}
	for _, path := range []string{paths.tmpMemfilePath, paths.tmpSnapfilePath, paths.oldMemfilePath, paths.oldSnapfilePath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove stale temporary snapshot file %q: %w", path, err)
		}
	}

	return paths, nil
}

func (p *atomicSnapshotPaths) commit() error {
	for _, path := range []string{p.tmpMemfilePath, p.tmpSnapfilePath} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("snapshot temporary file %q is not ready: %w", path, err)
		}
	}

	memHadOld, err := moveIfExists(p.memfilePath, p.oldMemfilePath)
	if err != nil {
		return fmt.Errorf("backup memfile %q: %w", p.memfilePath, err)
	}
	snapHadOld, err := moveIfExists(p.snapfilePath, p.oldSnapfilePath)
	if err != nil {
		rollbackAtomicMove(p.oldMemfilePath, p.memfilePath, memHadOld)
		return fmt.Errorf("backup snapfile %q: %w", p.snapfilePath, err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		rollbackAtomicMove(p.oldMemfilePath, p.memfilePath, memHadOld)
		rollbackAtomicMove(p.oldSnapfilePath, p.snapfilePath, snapHadOld)
	}()

	if err := os.Rename(p.tmpMemfilePath, p.memfilePath); err != nil {
		return fmt.Errorf("rename memfile %q to %q: %w", p.tmpMemfilePath, p.memfilePath, err)
	}
	if err := os.Rename(p.tmpSnapfilePath, p.snapfilePath); err != nil {
		return fmt.Errorf("rename snapfile %q to %q: %w", p.tmpSnapfilePath, p.snapfilePath, err)
	}
	committed = true
	p.committed = true
	removeIfExists(p.oldMemfilePath)
	removeIfExists(p.oldSnapfilePath)
	return nil
}

func (p *atomicSnapshotPaths) cleanup() {
	if p.committed {
		return
	}
	_ = os.Remove(p.tmpMemfilePath)
	_ = os.Remove(p.tmpSnapfilePath)
	_ = os.Remove(p.oldMemfilePath)
	_ = os.Remove(p.oldSnapfilePath)
}

func moveIfExists(src string, dst string) (bool, error) {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := os.Rename(src, dst); err != nil {
		return false, err
	}
	return true, nil
}

func rollbackAtomicMove(src string, dst string, shouldRollback bool) {
	if !shouldRollback {
		return
	}
	_ = os.Remove(dst)
	_ = os.Rename(src, dst)
}

func removeIfExists(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return
	}
}

func (d *FirecrackerDriver) Resume(ctx context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}
	if spec.GetSnapshot() == nil || spec.GetSnapshot().GetMemfilePath() == "" || spec.GetSnapshot().GetSnapfilePath() == "" {
		return nil, errors.New("runtime_spec.snapshot.memfile_path and snapfile_path are required for firecracker resume")
	}
	if err := validateFirecrackerSpec(spec); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	sandboxDir := layout.New(d.cfg.RootDir).SandboxDir(spec.GetSandboxId())
	if err := os.MkdirAll(sandboxDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sandbox directory: %w", err)
	}

	cmd, apiSocket, err := d.startFirecrackerLocked(spec)
	if err != nil {
		return nil, err
	}
	if err := d.waitForAPI(ctx, apiSocket); err != nil {
		_ = d.killLocked()
		return nil, d.withLogTail(err)
	}

	req := firecrackerSnapshotLoadRequest{
		SnapshotPath:        spec.GetSnapshot().GetSnapfilePath(),
		MemBackend:          firecrackerMemBackend{BackendPath: spec.GetSnapshot().GetMemfilePath(), BackendType: "File"},
		ResumeVM:            true,
		EnableDiffSnapshots: false,
	}
	if err := d.client.put(ctx, "/snapshot/load", req); err != nil {
		_ = d.killLocked()
		return nil, d.withLogTail(fmt.Errorf("load firecracker snapshot: %w", err))
	}

	info := &novitaboxv1.RuntimeInfo{
		SandboxId:         spec.GetSandboxId(),
		RuntimeType:       novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER,
		State:             novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING,
		Pid:               int64(cmd.Process.Pid),
		ShimSocketPath:    d.cfg.Boxshim.SocketPath,
		RuntimeSocketPath: apiSocket,
	}
	d.info = info
	d.spec = spec

	return cloneRuntimeInfo(info), nil
}

func (d *FirecrackerDriver) Kill(_ context.Context, sandboxID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkSandboxLocked(sandboxID); err != nil {
		return err
	}
	if err := d.killLocked(); err != nil {
		return err
	}
	d.info.State = novitaboxv1.RuntimeState_RUNTIME_STATE_EXITED
	d.info.Pid = 0
	d.info.ErrorMessage = ""
	return nil
}

func (d *FirecrackerDriver) Start(ctx context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	return d.Create(ctx, spec)
}

func (d *FirecrackerDriver) Stop(ctx context.Context, sandboxID string, timeout time.Duration) (*novitaboxv1.RuntimeInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkSandboxLocked(sandboxID); err != nil {
		return nil, err
	}

	if d.client != nil {
		req := firecrackerActionRequest{ActionType: "SendCtrlAltDel"}
		if err := d.client.put(ctx, "/actions", req); err != nil {
			d.logger.Warn("firecracker graceful shutdown failed", "sandbox_id", sandboxID, "error", err)
		}
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if err := d.waitProcessExitLocked(timeout); err != nil {
		return nil, err
	}

	d.info.State = novitaboxv1.RuntimeState_RUNTIME_STATE_STOPPED
	d.info.Pid = 0
	d.info.ErrorMessage = ""
	return cloneRuntimeInfo(d.info), nil
}

func (d *FirecrackerDriver) Reboot(ctx context.Context, sandboxID string, timeout time.Duration) (*novitaboxv1.RuntimeInfo, error) {
	if _, err := d.Stop(ctx, sandboxID, timeout); err != nil {
		return nil, err
	}

	d.mu.Lock()
	if d.spec == nil {
		d.mu.Unlock()
		return nil, errors.New("runtime spec is not available")
	}
	spec := d.spec
	d.mu.Unlock()

	return d.Create(ctx, spec)
}

func (d *FirecrackerDriver) Status(_ context.Context, sandboxID string) (*novitaboxv1.RuntimeInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.info == nil {
		return &novitaboxv1.RuntimeInfo{SandboxId: sandboxID, State: novitaboxv1.RuntimeState_RUNTIME_STATE_UNKNOWN}, nil
	}
	if sandboxID != "" && d.info.GetSandboxId() != sandboxID {
		return nil, fmt.Errorf("runtime sandbox mismatch: have %q, got %q", d.info.GetSandboxId(), sandboxID)
	}
	if d.cmd != nil && d.cmd.Process != nil && d.info.State == novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING {
		if err := d.checkProcessAliveLocked(); err != nil {
			d.info.State = novitaboxv1.RuntimeState_RUNTIME_STATE_EXITED
			d.info.Pid = 0
			d.info.ErrorMessage = err.Error()
		}
	}

	return cloneRuntimeInfo(d.info), nil
}

func (d *FirecrackerDriver) Capabilities(context.Context, novitaboxv1.RuntimeType) (*novitaboxv1.RuntimeCapabilities, error) {
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
		Jailer:            false,
	}, nil
}

func (d *FirecrackerDriver) configureMachine(ctx context.Context, spec *novitaboxv1.RuntimeSpec) error {
	machine := spec.GetMachine()
	vcpu := machine.GetVcpu()
	if vcpu == 0 {
		vcpu = 1
	}
	memoryMB := machine.GetMemoryMb()
	if memoryMB == 0 {
		memoryMB = 512
	}

	if err := d.client.put(ctx, "/machine-config", firecrackerMachineConfig{
		VCPUCount:  int64(vcpu),
		MemSizeMib: int64(memoryMB),
	}); err != nil {
		return fmt.Errorf("configure firecracker machine: %w", err)
	}

	if err := d.client.put(ctx, "/boot-source", firecrackerBootSource{
		KernelImagePath: spec.GetKernel().GetKernelPath(),
		BootArgs:        bootArgs(spec),
	}); err != nil {
		return fmt.Errorf("configure firecracker boot source: %w", err)
	}

	if err := d.client.put(ctx, "/drives/rootfs", firecrackerDrive{
		DriveID:      "rootfs",
		PathOnHost:   spec.GetRootfs().GetPath(),
		IsRootDevice: true,
		IsReadOnly:   spec.GetRootfs().GetReadonly(),
	}); err != nil {
		return fmt.Errorf("configure firecracker rootfs: %w", err)
	}

	if spec.GetNetwork() != nil && spec.GetNetwork().GetTapName() != "" {
		if err := d.client.put(ctx, "/network-interfaces/eth0", firecrackerNetworkInterface{
			IfaceID:     "eth0",
			HostDevName: spec.GetNetwork().GetTapName(),
			GuestMAC:    spec.GetNetwork().GetMac(),
		}); err != nil {
			return fmt.Errorf("configure firecracker network: %w", err)
		}
	}

	if err := d.client.put(ctx, "/actions", firecrackerActionRequest{ActionType: "InstanceStart"}); err != nil {
		return fmt.Errorf("start firecracker instance: %w", err)
	}

	return nil
}

func (d *FirecrackerDriver) startFirecrackerLocked(spec *novitaboxv1.RuntimeSpec) (*exec.Cmd, string, error) {
	sandboxDir := layout.New(d.cfg.RootDir).SandboxDir(spec.GetSandboxId())
	apiSocket := filepath.Join(sandboxDir, "fc.sock")
	if err := os.RemoveAll(apiSocket); err != nil {
		return nil, "", fmt.Errorf("remove stale firecracker api socket: %w", err)
	}

	logPath := filepath.Join(sandboxDir, "firecracker.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open firecracker log: %w", err)
	}

	cmd := firecrackerCommand(d.cfg.Firecracker.BinaryPath, apiSocket, spec.GetNetwork())
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, "", fmt.Errorf("start firecracker: %w", err)
	}
	logFile.Close()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	d.cmd = cmd
	d.waitCh = waitCh
	d.exited = false
	d.exitErr = nil
	d.client = newFirecrackerClient(apiSocket)
	d.logPath = logPath

	return cmd, apiSocket, nil
}

func firecrackerCommand(binaryPath string, apiSocket string, network *novitaboxv1.NetworkSpec) *exec.Cmd {
	args := []string{"--api-sock", apiSocket}
	if network == nil || network.GetNamespaceName() == "" {
		return exec.Command(binaryPath, args...)
	}
	netnsArgs := append([]string{"netns", "exec", network.GetNamespaceName(), binaryPath}, args...)
	return exec.Command("ip", netnsArgs...)
}

func (d *FirecrackerDriver) checkProcessAliveLocked() error {
	if d.cmd == nil || d.cmd.Process == nil {
		return d.withLogTail(errors.New("firecracker process is not running"))
	}
	d.refreshProcessExitLocked()
	if d.exited {
		if d.exitErr != nil {
			return d.withLogTail(fmt.Errorf("firecracker process exited: %w", d.exitErr))
		}
		return d.withLogTail(errors.New("firecracker process exited"))
	}
	if err := d.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return d.withLogTail(fmt.Errorf("firecracker process is not alive: %w", err))
	}

	return nil
}

func (d *FirecrackerDriver) refreshProcessExitLocked() {
	if d.waitCh == nil || d.exitErr != nil {
		return
	}

	select {
	case err := <-d.waitCh:
		d.exited = true
		d.exitErr = err
		d.waitCh = nil
	default:
	}
}

func (d *FirecrackerDriver) waitPostStartAliveLocked(ctx context.Context, window time.Duration) error {
	timer := time.NewTimer(window)
	defer timer.Stop()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := d.checkProcessAliveLocked(); err != nil {
			return fmt.Errorf("firecracker exited shortly after start: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-ticker.C:
		}
	}
}

func (d *FirecrackerDriver) waitForAPI(ctx context.Context, socketPath string) error {
	deadline := time.Now().Add(firecrackerAPITimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			conn, err := (&net.Dialer{Timeout: 50 * time.Millisecond}).DialContext(ctx, "unix", socketPath)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	return fmt.Errorf("wait for firecracker api socket %q timed out", socketPath)
}

func (d *FirecrackerDriver) checkSandboxLocked(sandboxID string) error {
	if sandboxID == "" {
		return errors.New("sandbox_id is required")
	}
	if d.info == nil {
		return fmt.Errorf("runtime for sandbox %q is not created", sandboxID)
	}
	if d.info.GetSandboxId() != sandboxID {
		return fmt.Errorf("runtime sandbox mismatch: have %q, got %q", d.info.GetSandboxId(), sandboxID)
	}
	return nil
}

func (d *FirecrackerDriver) killLocked() error {
	if d.cmd == nil || d.cmd.Process == nil {
		return nil
	}

	d.refreshProcessExitLocked()
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = d.cmd.Process.Kill()
		return fmt.Errorf("terminate firecracker: %w", err)
	}

	select {
	case err := <-d.processWaitChLocked():
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				return fmt.Errorf("wait firecracker exit: %w", err)
			}
		}
	case <-time.After(5 * time.Second):
		if err := d.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill firecracker: %w", err)
		}
		<-d.processWaitChLocked()
	}

	d.cmd = nil
	d.waitCh = nil
	d.exited = false
	d.exitErr = nil
	d.client = nil
	return nil
}

func (d *FirecrackerDriver) waitProcessExitLocked(timeout time.Duration) error {
	if d.cmd == nil || d.cmd.Process == nil {
		return nil
	}

	select {
	case err := <-d.processWaitChLocked():
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				return fmt.Errorf("wait firecracker exit: %w", err)
			}
		}
	case <-time.After(timeout):
		if err := d.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill firecracker after graceful shutdown timeout: %w", err)
		}
		<-d.processWaitChLocked()
	}

	d.cmd = nil
	d.waitCh = nil
	d.exited = false
	d.exitErr = nil
	d.client = nil
	return nil
}

func (d *FirecrackerDriver) processWaitChLocked() <-chan error {
	if d.waitCh != nil {
		return d.waitCh
	}

	done := make(chan error, 1)
	done <- d.exitErr
	return done
}

func (d *FirecrackerDriver) withLogTail(err error) error {
	if err == nil || d.logPath == "" {
		return err
	}
	tail := tailFile(d.logPath, 8192)
	if tail == "" {
		return err
	}

	return fmt.Errorf("%w; firecracker log tail: %s", err, tail)
}

func tailFile(path string, limit int64) string {
	if limit <= 0 {
		limit = 8192
	}

	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return ""
	}

	offset := info.Size() - limit
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ""
	}

	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

func validateFirecrackerSpec(spec *novitaboxv1.RuntimeSpec) error {
	if spec.GetRuntimeType() != novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER {
		return fmt.Errorf("firecracker driver cannot run runtime type %s", spec.GetRuntimeType().String())
	}
	if spec.GetKernel().GetKernelPath() == "" {
		return errors.New("runtime_spec.kernel.kernel_path is required for firecracker")
	}
	if spec.GetRootfs().GetPath() == "" {
		return errors.New("runtime_spec.rootfs.path is required for firecracker")
	}
	return nil
}

func bootArgs(spec *novitaboxv1.RuntimeSpec) string {
	args := append([]string(nil), spec.GetKernel().GetKernelArgs()...)
	if len(args) == 0 {
		args = []string{"reboot=k", "panic=1", "pci=off", "8250.nr_uarts=0", "root=/dev/vda", "rw", "quiet", "loglevel=0"}
	}
	if spec.GetKernel().GetInitPath() != "" && !hasKernelArg(args, "init=") {
		args = append(args, "init="+spec.GetKernel().GetInitPath())
	}
	if ipArg := networkKernelIPArg(spec.GetNetwork()); ipArg != "" && !hasKernelArg(args, "ip=") {
		args = append(args, ipArg)
	}
	return joinArgs(args)
}

func hasKernelArg(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func networkKernelIPArg(network *novitaboxv1.NetworkSpec) string {
	if network == nil || network.GetGuestIp() == "" || network.GetGatewayIp() == "" {
		return ""
	}
	return fmt.Sprintf("ip=%s::%s:255.255.255.252::eth0:off", network.GetGuestIp(), network.GetGatewayIp())
}

func joinArgs(args []string) string {
	var buf bytes.Buffer
	for i, arg := range args {
		if i > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(arg)
	}
	return buf.String()
}

type firecrackerClient struct {
	socketPath string
	httpClient *http.Client
}

func newFirecrackerClient(socketPath string) *firecrackerClient {
	return &firecrackerClient{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (c *firecrackerClient) get(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodGet, path, nil)
}

func (c *firecrackerClient) put(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPut, path, body)
}

func (c *firecrackerClient) patch(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPatch, path, body)
}

func (c *firecrackerClient) do(ctx context.Context, method string, path string, body any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal firecracker request: %w", err)
		}
		r = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, r)
	if err != nil {
		return fmt.Errorf("create firecracker request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("firecracker api %s %s failed: status=%d body=%s", method, path, resp.StatusCode, string(data))
}

type firecrackerMachineConfig struct {
	VCPUCount  int64 `json:"vcpu_count"`
	MemSizeMib int64 `json:"mem_size_mib"`
}

type firecrackerBootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args,omitempty"`
}

type firecrackerDrive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type firecrackerNetworkInterface struct {
	IfaceID     string `json:"iface_id"`
	HostDevName string `json:"host_dev_name"`
	GuestMAC    string `json:"guest_mac,omitempty"`
}

type firecrackerActionRequest struct {
	ActionType string `json:"action_type"`
}

type firecrackerVMStateRequest struct {
	State string `json:"state"`
}

type firecrackerSnapshotCreateRequest struct {
	SnapshotType string `json:"snapshot_type"`
	SnapshotPath string `json:"snapshot_path"`
	MemFilePath  string `json:"mem_file_path"`
}

type firecrackerMemBackend struct {
	BackendPath string `json:"backend_path"`
	BackendType string `json:"backend_type"`
}

type firecrackerSnapshotLoadRequest struct {
	SnapshotPath        string                `json:"snapshot_path"`
	MemBackend          firecrackerMemBackend `json:"mem_backend"`
	ResumeVM            bool                  `json:"resume_vm"`
	EnableDiffSnapshots bool                  `json:"enable_diff_snapshots"`
}
