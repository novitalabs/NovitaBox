package runtime

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
	"google.golang.org/protobuf/proto"
)

const (
	firecrackerAPITimeout      = 10 * time.Second
	firecrackerPostStartWindow = 1 * time.Second
	jailerGuestAPISocket       = "/fc.sock"
	jailerDefaultUID           = "65534"
	jailerDefaultGID           = "65534"
	jailerCgroupParent         = "novitabox"
	jailerCgroupPeriodUS       = uint32(100000)
	jailerPidsMax              = uint32(512)
	jailerMemoryOverheadMiB    = uint32(256)
	jailerNoFileLimit          = "4096"
	jailerAgentDrivePath       = "/agent/boxd.ext4"
	jailerExtraDriveDir        = "/extra-drives"
	jailerNetNSDir             = "/var/run/netns"
)

type firecrackerLaunch struct {
	cmd       *exec.Cmd
	apiSocket string
	logPath   string
	jailer    *firecrackerJailerRuntime
}

type firecrackerJailerRuntime struct {
	id         string
	baseDir    string
	rootDir    string
	rootLink   string
	apiLink    string
	uid        int
	gid        int
	cgroup     firecrackerJailerCgroup
	bindMounts []jailerBindMount
	pathMap    []jailerPathMapping
}

type firecrackerJailerCgroup struct {
	version string
	parent  string
	id      string
}

type firecrackerJailerSpec struct {
	binaryPath string
	chrootDir  string
	uid        string
	gid        string
	newPIDNS   bool
	netnsPath  string

	cgroupVersion string
	parentCgroup  string
	cgroups       []string
	resourceLimit []string
}

type jailerBindMount struct {
	hostPath  string
	guestPath string
	isDir     bool
}

type jailerPathMapping struct {
	hostPath  string
	guestPath string
	isDir     bool
}

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
	jailer  *firecrackerJailerRuntime
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

	launch, err := d.prepareFirecrackerLaunch(spec, sandboxDir)
	if err != nil {
		return nil, err
	}
	if launch.jailer != nil {
		if err := launch.jailer.prepareMounts(spec); err != nil {
			return nil, err
		}
	}

	apiSocket, err := d.startFirecrackerLocked(launch)
	if err != nil {
		if launch.jailer != nil {
			_ = launch.jailer.cleanup()
		}
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
		Pid:               int64(d.runtimePIDLocked()),
		ShimSocketPath:    d.cfg.Boxshim.SocketPath,
		RuntimeSocketPath: apiSocket,
	}
	d.info = info
	d.spec = spec

	d.logger.Info("created firecracker runtime",
		"sandbox_id", spec.GetSandboxId(),
		"pid", info.GetPid(),
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

	if err := d.client.PauseVM(ctx); err != nil {
		return nil, d.withLogTail(fmt.Errorf("pause firecracker vm: %w", err))
	}

	paths, err := prepareAtomicSnapshotPaths(d.spec.GetSnapshot().GetMemfilePath(), d.spec.GetSnapshot().GetSnapfilePath())
	if err != nil {
		return nil, err
	}
	defer paths.cleanup()

	req := firecrackerSnapshotCreateRequest{
		SnapshotType: "Full",
		SnapshotPath: d.runtimePath(paths.tmpSnapfilePath),
		MemFilePath:  d.runtimePath(paths.tmpMemfilePath),
	}
	if err := d.client.CreateSnapshot(ctx, req); err != nil {
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

	launch, err := d.prepareFirecrackerLaunch(spec, sandboxDir)
	if err != nil {
		return nil, err
	}
	if launch.jailer != nil {
		if err := launch.jailer.prepareMounts(spec); err != nil {
			return nil, err
		}
	}

	apiSocket, err := d.startFirecrackerLocked(launch)
	if err != nil {
		if launch.jailer != nil {
			_ = launch.jailer.cleanup()
		}
		return nil, err
	}
	if err := d.waitForAPI(ctx, apiSocket); err != nil {
		_ = d.killLocked()
		return nil, d.withLogTail(err)
	}

	req := firecrackerSnapshotLoadRequest{
		SnapshotPath:        d.runtimePath(spec.GetSnapshot().GetSnapfilePath()),
		MemBackend:          firecrackerMemBackend{BackendPath: d.runtimePath(spec.GetSnapshot().GetMemfilePath()), BackendType: "File"},
		ResumeVM:            true,
		EnableDiffSnapshots: false,
	}
	if err := d.client.LoadSnapshot(ctx, req); err != nil {
		_ = d.killLocked()
		return nil, d.withLogTail(fmt.Errorf("load firecracker snapshot: %w", err))
	}
	if err := d.waitPostStartAliveLocked(ctx, firecrackerPostStartWindow); err != nil {
		_ = d.killLocked()
		return nil, d.withLogTail(err)
	}

	info := &novitaboxv1.RuntimeInfo{
		SandboxId:         spec.GetSandboxId(),
		RuntimeType:       novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER,
		State:             novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING,
		Pid:               int64(d.runtimePIDLocked()),
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
		if err := d.client.SendCtrlAltDel(ctx); err != nil {
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
		Jailer:            true,
	}, nil
}

func (d *FirecrackerDriver) configureMachine(ctx context.Context, spec *novitaboxv1.RuntimeSpec) error {
	spec = d.runtimeSpec(spec)
	machine := spec.GetMachine()
	vcpu := machine.GetVcpu()
	if vcpu == 0 {
		vcpu = 1
	}
	memoryMB := machine.GetMemoryMb()
	if memoryMB == 0 {
		memoryMB = 512
	}

	if err := d.client.PutMachineConfig(ctx, firecrackerMachineConfig{
		VCPUCount:  int64(vcpu),
		MemSizeMib: int64(memoryMB),
	}); err != nil {
		return fmt.Errorf("configure firecracker machine: %w", err)
	}

	if err := d.client.PutBootSource(ctx, firecrackerBootSource{
		KernelImagePath: spec.GetKernel().GetKernelPath(),
		BootArgs:        bootArgs(spec),
	}); err != nil {
		return fmt.Errorf("configure firecracker boot source: %w", err)
	}

	if err := d.client.PutDrive(ctx, firecrackerDrive{
		DriveID:      "rootfs",
		PathOnHost:   spec.GetRootfs().GetPath(),
		IsRootDevice: true,
		IsReadOnly:   spec.GetRootfs().GetReadonly(),
	}); err != nil {
		return fmt.Errorf("configure firecracker rootfs: %w", err)
	}
	for _, drive := range spec.GetExtraDrives() {
		if drive.GetDriveId() == "" {
			return errors.New("runtime_spec.extra_drives.drive_id is required for firecracker")
		}
		if drive.GetPath() == "" {
			return fmt.Errorf("runtime_spec.extra_drives[%s].path is required for firecracker", drive.GetDriveId())
		}
		if err := d.client.PutDrive(ctx, firecrackerDrive{
			DriveID:      drive.GetDriveId(),
			PathOnHost:   drive.GetPath(),
			IsRootDevice: false,
			IsReadOnly:   drive.GetReadonly(),
		}); err != nil {
			return fmt.Errorf("configure firecracker drive %q: %w", drive.GetDriveId(), err)
		}
	}

	if spec.GetNetwork() != nil && spec.GetNetwork().GetTapName() != "" {
		if err := d.client.PutNetworkInterface(ctx, firecrackerNetworkInterface{
			IfaceID:     "eth0",
			HostDevName: spec.GetNetwork().GetTapName(),
			GuestMAC:    spec.GetNetwork().GetMac(),
		}); err != nil {
			return fmt.Errorf("configure firecracker network: %w", err)
		}
	}

	if err := d.client.StartInstance(ctx); err != nil {
		return fmt.Errorf("start firecracker instance: %w", err)
	}

	return nil
}

func (d *FirecrackerDriver) startFirecrackerLocked(launch *firecrackerLaunch) (string, error) {
	logPath := launch.logPath
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("open firecracker log: %w", err)
	}
	cmd := launch.cmd
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return "", fmt.Errorf("start firecracker: %w", err)
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
	d.client = newFirecrackerClient(launch.apiSocket)
	d.logPath = logPath
	d.jailer = launch.jailer

	return launch.apiSocket, nil
}

func (d *FirecrackerDriver) prepareFirecrackerLaunch(spec *novitaboxv1.RuntimeSpec, sandboxDir string) (*firecrackerLaunch, error) {
	jailerSpec, useJailer, err := d.effectiveJailerSpec(spec)
	if err != nil {
		return nil, err
	}
	if !useJailer {
		apiSocket := filepath.Join(sandboxDir, "fc.sock")
		if err := os.RemoveAll(apiSocket); err != nil {
			return nil, fmt.Errorf("remove stale firecracker api socket: %w", err)
		}
		return &firecrackerLaunch{
			cmd:       firecrackerCommand(d.cfg.Firecracker.BinaryPath, apiSocket, spec.GetNetwork()),
			apiSocket: apiSocket,
			logPath:   filepath.Join(sandboxDir, "firecracker.log"),
		}, nil
	}

	jailer, err := newFirecrackerJailerRuntime(spec.GetSandboxId(), jailerSpec)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(jailer.baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create jailer base directory: %w", err)
	}
	if err := unmountUnder(jailer.instanceDir()); err != nil {
		return nil, fmt.Errorf("unmount stale jailer mounts: %w", err)
	}
	if err := os.RemoveAll(jailer.instanceDir()); err != nil {
		return nil, fmt.Errorf("remove stale jailer directory: %w", err)
	}
	if err := os.MkdirAll(jailer.rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create jailer root directory: %w", err)
	}
	guestAPISocket := filepath.Join(jailer.rootDir, strings.TrimPrefix(jailerGuestAPISocket, "/"))
	if err := os.RemoveAll(guestAPISocket); err != nil {
		return nil, fmt.Errorf("remove stale jailer api socket: %w", err)
	}
	apiSocket := jailerHostAPISocketPath(spec.GetSandboxId(), guestAPISocket)
	if err := os.RemoveAll(apiSocket); err != nil {
		return nil, fmt.Errorf("remove stale firecracker api socket link: %w", err)
	}
	if err := os.Symlink(guestAPISocket, apiSocket); err != nil {
		return nil, fmt.Errorf("create firecracker api socket link: %w", err)
	}
	jailer.apiLink = apiSocket

	return &firecrackerLaunch{
		cmd:       firecrackerJailerNetworkCommand(d.cfg.Firecracker.BinaryPath, jailerSpec, spec.GetSandboxId(), jailerGuestAPISocket, spec.GetNetwork()),
		apiSocket: apiSocket,
		logPath:   filepath.Join(sandboxDir, "firecracker.log"),
		jailer:    jailer,
	}, nil
}

func firecrackerCommand(binaryPath string, apiSocket string, network *novitaboxv1.NetworkSpec) *exec.Cmd {
	args := []string{"--api-sock", apiSocket}
	if network == nil || network.GetNamespaceName() == "" {
		return exec.Command(binaryPath, args...)
	}
	netnsArgs := append([]string{"netns", "exec", network.GetNamespaceName(), binaryPath}, args...)
	return exec.Command("ip", netnsArgs...)
}

func firecrackerJailerCommand(firecrackerPath string, jailer *firecrackerJailerSpec, sandboxID string, guestAPISocket string) *exec.Cmd {
	args := []string{
		"--id", sandboxID,
		"--exec-file", firecrackerPath,
		"--uid", jailer.uid,
		"--gid", jailer.gid,
		"--chroot-base-dir", jailer.chrootDir,
	}
	if jailer.newPIDNS {
		args = append(args, "--new-pid-ns")
	}
	if jailer.netnsPath != "" {
		args = append(args, "--netns", jailer.netnsPath)
	}
	if jailer.cgroupVersion != "" {
		args = append(args, "--cgroup-version", jailer.cgroupVersion)
	}
	if jailer.parentCgroup != "" {
		args = append(args, "--parent-cgroup", jailer.parentCgroup)
	}
	for _, cgroup := range jailer.cgroups {
		args = append(args, "--cgroup", cgroup)
	}
	for _, limit := range jailer.resourceLimit {
		args = append(args, "--resource-limit", limit)
	}
	args = append(args, "--", "--api-sock", guestAPISocket)
	return exec.Command(jailer.binaryPath, args...)
}

func firecrackerJailerNetworkCommand(firecrackerPath string, jailer *firecrackerJailerSpec, sandboxID string, guestAPISocket string, network *novitaboxv1.NetworkSpec) *exec.Cmd {
	if network != nil && network.GetNamespaceName() != "" {
		jailer.netnsPath = filepath.Join(jailerNetNSDir, network.GetNamespaceName())
	}
	return firecrackerJailerCommand(firecrackerPath, jailer, sandboxID, guestAPISocket)
}

func jailerHostAPISocketPath(sandboxID string, guestAPISocket string) string {
	sum := sha1.Sum([]byte(sandboxID + "\x00" + guestAPISocket))
	return filepath.Join(os.TempDir(), "novitabox-fc-"+hex.EncodeToString(sum[:8])+".sock")
}

func (d *FirecrackerDriver) effectiveJailerSpec(spec *novitaboxv1.RuntimeSpec) (*firecrackerJailerSpec, bool, error) {
	out := &firecrackerJailerSpec{
		binaryPath:    config.DefaultFirecrackerJailerBinaryPath(d.cfg.RootDir),
		chrootDir:     layout.New(d.cfg.RootDir).SandboxJailerDir(spec.GetSandboxId()),
		uid:           jailerDefaultUID,
		gid:           defaultJailerGID(),
		newPIDNS:      true,
		cgroupVersion: detectJailerCgroupVersion(),
		parentCgroup:  jailerCgroupParent,
		resourceLimit: []string{"no-file=" + jailerNoFileLimit},
	}
	out.cgroups = jailerCgroups(out.cgroupVersion, spec.GetMachine())
	if out.cgroupVersion == "" {
		out.parentCgroup = ""
	}
	if out.binaryPath == "" {
		return nil, false, errors.New("firecracker jailer binary path is required")
	}
	return out, true, nil
}

func defaultJailerGID() string {
	var stat syscall.Stat_t
	if err := syscall.Stat("/dev/kvm", &stat); err == nil && stat.Gid > 0 {
		return strconv.FormatUint(uint64(stat.Gid), 10)
	}
	return jailerDefaultGID
}

func detectJailerCgroupVersion() string {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return "2"
	}
	if _, err := os.Stat("/sys/fs/cgroup/cpu"); err == nil {
		return "1"
	}
	if _, err := os.Stat("/sys/fs/cgroup/memory"); err == nil {
		return "1"
	}
	return ""
}

func jailerCgroups(version string, machine *novitaboxv1.MachineSpec) []string {
	if version == "" {
		return nil
	}
	vcpu := machine.GetVcpu()
	if vcpu == 0 {
		vcpu = 1
	}
	memoryMB := machine.GetMemoryMb()
	if memoryMB == 0 {
		memoryMB = 512
	}
	memoryBytes := uint64(memoryMB+jailerMemoryOverheadMiB) * 1024 * 1024
	cpuQuota := uint64(vcpu) * uint64(jailerCgroupPeriodUS)

	switch version {
	case "2":
		return []string{
			fmt.Sprintf("memory.max=%d", memoryBytes),
			fmt.Sprintf("cpu.max=%d %d", cpuQuota, jailerCgroupPeriodUS),
			fmt.Sprintf("pids.max=%d", jailerPidsMax),
		}
	case "1":
		return []string{
			fmt.Sprintf("memory.limit_in_bytes=%d", memoryBytes),
			fmt.Sprintf("cpu.cfs_period_us=%d", jailerCgroupPeriodUS),
			fmt.Sprintf("cpu.cfs_quota_us=%d", cpuQuota),
			fmt.Sprintf("pids.max=%d", jailerPidsMax),
		}
	default:
		return nil
	}
}

func newFirecrackerJailerRuntime(sandboxID string, spec *firecrackerJailerSpec) (*firecrackerJailerRuntime, error) {
	if sandboxID == "" {
		return nil, errors.New("runtime_spec.sandbox_id is required for firecracker jailer")
	}
	if spec.chrootDir == "" {
		return nil, errors.New("firecracker jailer chroot directory is required")
	}
	uid, err := strconv.Atoi(spec.uid)
	if err != nil || uid < 0 {
		return nil, fmt.Errorf("invalid firecracker jailer uid %q", spec.uid)
	}
	gid, err := strconv.Atoi(spec.gid)
	if err != nil || gid < 0 {
		return nil, fmt.Errorf("invalid firecracker jailer gid %q", spec.gid)
	}
	instanceDir := filepath.Join(spec.chrootDir, "firecracker", sandboxID)
	return &firecrackerJailerRuntime{
		id:       sandboxID,
		baseDir:  spec.chrootDir,
		rootDir:  filepath.Join(instanceDir, "root"),
		rootLink: filepath.Join(spec.chrootDir, "root"),
		uid:      uid,
		gid:      gid,
		cgroup: firecrackerJailerCgroup{
			version: spec.cgroupVersion,
			parent:  spec.parentCgroup,
			id:      sandboxID,
		},
	}, nil
}

func (j *firecrackerJailerRuntime) instanceDir() string {
	return filepath.Join(j.baseDir, "firecracker", j.id)
}

func (j *firecrackerJailerRuntime) prepareMounts(spec *novitaboxv1.RuntimeSpec) error {
	if j == nil {
		return nil
	}
	if err := j.ensureRootLink(); err != nil {
		return err
	}
	if spec.GetKernel().GetKernelPath() != "" {
		if err := j.bindFile(spec.GetKernel().GetKernelPath(), "/kernel"); err != nil {
			return fmt.Errorf("bind jailer kernel: %w", err)
		}
	}
	if spec.GetRootfs().GetPath() != "" {
		if !spec.GetRootfs().GetReadonly() {
			if err := j.ensureWritableByJailer(spec.GetRootfs().GetPath(), false); err != nil {
				return fmt.Errorf("prepare jailer rootfs permissions: %w", err)
			}
		}
		if err := j.bindFile(spec.GetRootfs().GetPath(), "/rootfs.ext4"); err != nil {
			return fmt.Errorf("bind jailer rootfs: %w", err)
		}
	}
	for _, drive := range spec.GetExtraDrives() {
		if drive.GetPath() == "" || drive.GetDriveId() == "" {
			continue
		}
		if !drive.GetReadonly() {
			if err := j.ensureWritableByJailer(drive.GetPath(), false); err != nil {
				return fmt.Errorf("prepare jailer drive %q permissions: %w", drive.GetDriveId(), err)
			}
		}
		if err := j.bindFile(drive.GetPath(), jailerGuestDrivePath(drive)); err != nil {
			return fmt.Errorf("bind jailer drive %q: %w", drive.GetDriveId(), err)
		}
	}
	memfilePath := spec.GetSnapshot().GetMemfilePath()
	snapfilePath := spec.GetSnapshot().GetSnapfilePath()
	switch {
	case memfilePath != "" && snapfilePath != "" && filepath.Clean(filepath.Dir(memfilePath)) != filepath.Clean(filepath.Dir(snapfilePath)):
		if err := j.ensureWritableByJailer(filepath.Dir(memfilePath), true); err != nil {
			return fmt.Errorf("prepare jailer memfile directory permissions: %w", err)
		}
		if err := j.ensureWritableByJailer(filepath.Dir(snapfilePath), true); err != nil {
			return fmt.Errorf("prepare jailer snapfile directory permissions: %w", err)
		}
		if err := j.bindDir(filepath.Dir(memfilePath), "/snapshot/mem"); err != nil {
			return fmt.Errorf("bind jailer memfile directory: %w", err)
		}
		if err := j.bindDir(filepath.Dir(snapfilePath), "/snapshot/snap"); err != nil {
			return fmt.Errorf("bind jailer snapfile directory: %w", err)
		}
	case memfilePath != "":
		if err := j.ensureWritableByJailer(filepath.Dir(memfilePath), true); err != nil {
			return fmt.Errorf("prepare jailer snapshot directory permissions: %w", err)
		}
		if err := j.bindDir(filepath.Dir(memfilePath), "/snapshot"); err != nil {
			return fmt.Errorf("bind jailer snapshot directory: %w", err)
		}
	case snapfilePath != "":
		if err := j.ensureWritableByJailer(filepath.Dir(snapfilePath), true); err != nil {
			return fmt.Errorf("prepare jailer snapshot directory permissions: %w", err)
		}
		if err := j.bindDir(filepath.Dir(snapfilePath), "/snapshot"); err != nil {
			return fmt.Errorf("bind jailer snapshot directory: %w", err)
		}
	}
	return nil
}

func (j *firecrackerJailerRuntime) ensureWritableByJailer(path string, isDir bool) error {
	if j.uid == 0 && j.gid == 0 {
		return nil
	}
	if isDir {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	if err := os.Chown(path, j.uid, j.gid); err != nil {
		return err
	}
	return nil
}

func (j *firecrackerJailerRuntime) ensureRootLink() error {
	if j.rootLink == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(j.rootLink), 0o755); err != nil {
		return fmt.Errorf("create jailer link directory: %w", err)
	}
	if err := os.Remove(j.rootLink); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale jailer root link: %w", err)
	}
	if err := os.Symlink(j.rootDir, j.rootLink); err != nil {
		return fmt.Errorf("create jailer root link: %w", err)
	}
	return nil
}

func (j *firecrackerJailerRuntime) bindFile(hostPath string, guestPath string) error {
	return j.bind(hostPath, guestPath, false)
}

func jailerGuestDrivePath(drive *novitaboxv1.DriveSpec) string {
	if drive.GetDriveId() == "agent" {
		return jailerAgentDrivePath
	}
	return pathJoinGuest(jailerExtraDriveDir, drive.GetDriveId()+filepath.Ext(drive.GetPath()))
}

func (j *firecrackerJailerRuntime) bindDir(hostPath string, guestPath string) error {
	return j.bind(hostPath, guestPath, true)
}

func (j *firecrackerJailerRuntime) bind(hostPath string, guestPath string, isDir bool) error {
	hostPath = filepath.Clean(hostPath)
	guestPath = cleanGuestPath(guestPath)
	for _, mapping := range j.pathMap {
		if mapping.hostPath == hostPath && mapping.guestPath == guestPath && mapping.isDir == isDir {
			return nil
		}
	}

	target := filepath.Join(j.rootDir, strings.TrimPrefix(guestPath, "/"))
	if isDir {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("create bind target %q: %w", target, err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create bind target directory %q: %w", filepath.Dir(target), err)
		}
		file, err := os.OpenFile(target, os.O_CREATE, 0o644)
		if err != nil {
			return fmt.Errorf("create bind target %q: %w", target, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close bind target %q: %w", target, err)
		}
	}
	if err := runMountBind(hostPath, target); err != nil {
		return err
	}
	j.bindMounts = append(j.bindMounts, jailerBindMount{hostPath: hostPath, guestPath: target, isDir: isDir})
	j.pathMap = append(j.pathMap, jailerPathMapping{hostPath: hostPath, guestPath: guestPath, isDir: isDir})
	return nil
}

func runMountBind(src string, dst string) error {
	cmd := exec.Command("mount", "--bind", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount --bind %q %q: %w: %s", src, dst, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func unmountUnder(root string) error {
	mounts, err := mountPointsUnder(root)
	if err != nil {
		return err
	}
	var errs []string
	for _, target := range mounts {
		cmd := exec.Command("umount", "-l", target)
		if out, err := cmd.CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("umount %q: %v: %s", target, err, strings.TrimSpace(string(out))))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func mountPointsUnder(root string) ([]string, error) {
	root = filepath.Clean(root)
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var mounts []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		target := decodeMountInfoPath(fields[4])
		if target == root || strings.HasPrefix(target, root+string(os.PathSeparator)) {
			mounts = append(mounts, target)
		}
	}
	sort.Slice(mounts, func(i, j int) bool {
		return len(mounts[i]) > len(mounts[j])
	})
	return mounts, nil
}

func decodeMountInfoPath(path string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(path)
}

func (j *firecrackerJailerRuntime) cleanup() error {
	var errs []string
	if err := j.terminate(2 * time.Second); err != nil {
		errs = append(errs, err.Error())
	}
	for i := len(j.bindMounts) - 1; i >= 0; i-- {
		target := j.bindMounts[i].guestPath
		cmd := exec.Command("umount", "-l", target)
		if out, err := cmd.CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("umount %q: %v: %s", target, err, strings.TrimSpace(string(out))))
		}
	}
	if err := os.RemoveAll(j.instanceDir()); err != nil {
		errs = append(errs, fmt.Sprintf("remove jailer dir %q: %v", j.instanceDir(), err))
	}
	if j.rootLink != "" {
		if err := os.Remove(j.rootLink); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("remove jailer root link %q: %v", j.rootLink, err))
		}
	}
	if j.apiLink != "" {
		if err := os.Remove(j.apiLink); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("remove firecracker api socket link %q: %v", j.apiLink, err))
		}
	}
	j.bindMounts = nil
	j.pathMap = nil
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (j *firecrackerJailerRuntime) terminate(timeout time.Duration) error {
	if j == nil {
		return nil
	}
	pids := j.livePIDs()
	if len(pids) == 0 {
		return nil
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(j.livePIDs()) == 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, pid := range j.livePIDs() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if len(j.livePIDs()) == 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("terminate jailer cgroup %q: processes still alive: %v", filepath.Join(j.cgroup.parent, j.cgroup.id), j.livePIDs())
}

func (j *firecrackerJailerRuntime) waitExit(timeout time.Duration) error {
	if j == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(j.livePIDs()) == 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return j.terminate(2 * time.Second)
}

func (d *FirecrackerDriver) runtimeSpec(spec *novitaboxv1.RuntimeSpec) *novitaboxv1.RuntimeSpec {
	if d.jailer == nil {
		return spec
	}
	out := proto.Clone(spec).(*novitaboxv1.RuntimeSpec)
	if out.GetKernel().GetKernelPath() != "" {
		out.Kernel.KernelPath = d.runtimePath(out.GetKernel().GetKernelPath())
	}
	if out.GetRootfs().GetPath() != "" {
		out.Rootfs.Path = d.runtimePath(out.GetRootfs().GetPath())
	}
	if out.GetSnapshot().GetMemfilePath() != "" {
		out.Snapshot.MemfilePath = d.runtimePath(out.GetSnapshot().GetMemfilePath())
	}
	if out.GetSnapshot().GetSnapfilePath() != "" {
		out.Snapshot.SnapfilePath = d.runtimePath(out.GetSnapshot().GetSnapfilePath())
	}
	for _, drive := range out.GetExtraDrives() {
		if drive.GetPath() != "" {
			drive.Path = d.runtimePath(drive.GetPath())
		}
	}
	return out
}

func (d *FirecrackerDriver) runtimePath(path string) string {
	if d.jailer == nil || path == "" {
		return path
	}
	return d.jailer.runtimePath(path)
}

func (j *firecrackerJailerRuntime) runtimePath(path string) string {
	path = filepath.Clean(path)
	for _, mapping := range j.pathMap {
		if mapping.isDir {
			if path == mapping.hostPath {
				return mapping.guestPath
			}
			if rel, err := filepath.Rel(mapping.hostPath, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
				return pathJoinGuest(mapping.guestPath, filepath.ToSlash(rel))
			}
			continue
		}
		if path == mapping.hostPath {
			return mapping.guestPath
		}
	}
	return path
}

func cleanGuestPath(path string) string {
	if path == "" {
		return "/"
	}
	return "/" + strings.TrimPrefix(filepath.Clean(path), "/")
}

func pathJoinGuest(base string, elems ...string) string {
	parts := append([]string{base}, elems...)
	return cleanGuestPath(filepath.Join(parts...))
}

func (d *FirecrackerDriver) runtimePIDLocked() int {
	if d.jailer != nil {
		if pid, ok := d.jailer.firstLivePID(); ok {
			return pid
		}
	}
	if d.cmd != nil && d.cmd.Process != nil {
		return d.cmd.Process.Pid
	}
	return 0
}

func (d *FirecrackerDriver) checkProcessAliveLocked() error {
	if d.cmd == nil || d.cmd.Process == nil {
		return d.withLogTail(errors.New("firecracker process is not running"))
	}
	d.refreshProcessExitLocked()
	if d.jailer != nil {
		if _, ok := d.jailer.firstLivePID(); ok {
			return nil
		}
	}
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

func (j *firecrackerJailerRuntime) firstLivePID() (int, bool) {
	for _, pid := range j.livePIDs() {
		return pid, true
	}
	return 0, false
}

func (j *firecrackerJailerRuntime) livePIDs() []int {
	pids := j.cgroupPIDs()
	live := make([]int, 0, len(pids))
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		if err := syscall.Kill(pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
			live = append(live, pid)
		}
	}
	return live
}

func (j *firecrackerJailerRuntime) cgroupPIDs() []int {
	seen := map[int]struct{}{}
	var pids []int
	for _, path := range j.cgroupProcsPaths() {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(raw)) {
			pid, err := strconv.Atoi(field)
			if err != nil || pid <= 0 {
				continue
			}
			if _, ok := seen[pid]; ok {
				continue
			}
			seen[pid] = struct{}{}
			pids = append(pids, pid)
		}
	}
	return pids
}

func (j *firecrackerJailerRuntime) cgroupProcsPaths() []string {
	if j.cgroup.parent == "" || j.cgroup.id == "" {
		return nil
	}
	switch j.cgroup.version {
	case "2":
		return []string{filepath.Join("/sys/fs/cgroup", j.cgroup.parent, j.cgroup.id, "cgroup.procs")}
	case "1":
		controllers := []string{"cpu", "memory", "pids"}
		paths := make([]string, 0, len(controllers))
		for _, controller := range controllers {
			paths = append(paths, filepath.Join("/sys/fs/cgroup", controller, j.cgroup.parent, j.cgroup.id, "cgroup.procs"))
		}
		return paths
	default:
		return nil
	}
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
	var lastStatErr error
	var lastDialErr error
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			conn, err := (&net.Dialer{Timeout: 50 * time.Millisecond}).DialContext(ctx, "unix", socketPath)
			if err == nil {
				_ = conn.Close()
				return nil
			}
			lastDialErr = err
		} else {
			lastStatErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	return fmt.Errorf("wait for firecracker api socket %q timed out: last stat error: %v; last dial error: %v", socketPath, lastStatErr, lastDialErr)
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

	if d.jailer != nil {
		if err := d.jailer.terminate(5 * time.Second); err != nil {
			return err
		}
	}
	d.refreshProcessExitLocked()
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = d.cmd.Process.Kill()
		if d.jailer == nil {
			return fmt.Errorf("terminate firecracker: %w", err)
		}
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
			if d.jailer == nil {
				return fmt.Errorf("kill firecracker: %w", err)
			}
		}
		<-d.processWaitChLocked()
	}

	d.cmd = nil
	d.waitCh = nil
	d.exited = false
	d.exitErr = nil
	d.client = nil
	if err := d.cleanupJailerLocked(); err != nil {
		return err
	}
	return nil
}

func (d *FirecrackerDriver) waitProcessExitLocked(timeout time.Duration) error {
	if d.cmd == nil || d.cmd.Process == nil {
		return nil
	}

	if d.jailer != nil {
		if err := d.jailer.waitExit(timeout); err != nil {
			return err
		}
		d.refreshProcessExitLocked()
		d.cmd = nil
		d.waitCh = nil
		d.exited = false
		d.exitErr = nil
		d.client = nil
		if err := d.cleanupJailerLocked(); err != nil {
			return err
		}
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
	if err := d.cleanupJailerLocked(); err != nil {
		return err
	}
	return nil
}

func (d *FirecrackerDriver) cleanupJailerLocked() error {
	if d.jailer == nil {
		return nil
	}
	err := d.jailer.cleanup()
	d.jailer = nil
	if err != nil {
		return fmt.Errorf("cleanup firecracker jailer: %w", err)
	}
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
