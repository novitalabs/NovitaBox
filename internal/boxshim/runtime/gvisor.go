package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
)

const (
	gvisorStateDirName = "runsc"
	gvisorBundleName   = "bundle"
	gvisorLogName      = "runsc.log"
	gvisorNetNSDir     = "/var/run/netns"
)

type GVisorDriver struct {
	cfg    config.Config
	logger *log.Logger

	mu      sync.Mutex
	info    *novitaboxv1.RuntimeInfo
	spec    *novitaboxv1.RuntimeSpec
	stopped bool
}

func NewGVisorDriver(cfg config.Config, logger *log.Logger) *GVisorDriver {
	return &GVisorDriver{cfg: cfg, logger: logger}
}

func (d *GVisorDriver) Create(ctx context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	return d.start(ctx, spec, "create")
}

func (d *GVisorDriver) Start(ctx context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	return d.start(ctx, spec, "start")
}

func (d *GVisorDriver) Resume(context.Context, *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	return nil, errors.New("gvisor resume is not supported yet")
}

func (d *GVisorDriver) Pause(context.Context, string) (*novitaboxv1.RuntimeInfo, error) {
	return nil, errors.New("gvisor pause is not supported yet")
}

func (d *GVisorDriver) Kill(ctx context.Context, sandboxID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkSandboxLocked(sandboxID); err != nil {
		return err
	}
	if err := d.stopLocked(ctx, sandboxID, 0, true); err != nil {
		return err
	}
	d.info.State = novitaboxv1.RuntimeState_RUNTIME_STATE_EXITED
	d.info.Pid = 0
	d.stopped = true
	return nil
}

func (d *GVisorDriver) Stop(ctx context.Context, sandboxID string, timeout time.Duration) (*novitaboxv1.RuntimeInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkSandboxLocked(sandboxID); err != nil {
		return nil, err
	}
	if err := d.stopLocked(ctx, sandboxID, timeout, false); err != nil {
		return nil, err
	}
	d.info.State = novitaboxv1.RuntimeState_RUNTIME_STATE_STOPPED
	d.info.Pid = 0
	d.stopped = true
	return cloneRuntimeInfo(d.info), nil
}

func (d *GVisorDriver) Reboot(ctx context.Context, sandboxID string, timeout time.Duration) (*novitaboxv1.RuntimeInfo, error) {
	d.mu.Lock()
	if err := d.checkSandboxLocked(sandboxID); err != nil {
		d.mu.Unlock()
		return nil, err
	}
	spec := d.spec
	if err := d.stopLocked(ctx, sandboxID, timeout, false); err != nil {
		d.mu.Unlock()
		return nil, err
	}
	d.mu.Unlock()

	return d.start(ctx, spec, "reboot")
}

func (d *GVisorDriver) Status(_ context.Context, sandboxID string) (*novitaboxv1.RuntimeInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.info == nil {
		return &novitaboxv1.RuntimeInfo{
			SandboxId:   sandboxID,
			RuntimeType: novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER,
			State:       novitaboxv1.RuntimeState_RUNTIME_STATE_UNKNOWN,
		}, nil
	}
	if sandboxID != "" && d.info.GetSandboxId() != sandboxID {
		return nil, fmt.Errorf("runtime sandbox mismatch: have %q, got %q", d.info.GetSandboxId(), sandboxID)
	}

	return cloneRuntimeInfo(d.info), nil
}

func (d *GVisorDriver) Capabilities(context.Context, novitaboxv1.RuntimeType) (*novitaboxv1.RuntimeCapabilities, error) {
	return gvisorCapabilities(), nil
}

func (d *GVisorDriver) UpdateBalloon(context.Context, string, uint32) (*novitaboxv1.BalloonConfig, error) {
	return nil, errors.New("balloon is not supported by this runtime")
}

func (d *GVisorDriver) GetBalloon(context.Context, string) (*novitaboxv1.BalloonConfig, error) {
	return nil, errors.New("balloon is not supported by this runtime")
}

func (d *GVisorDriver) GetBalloonStats(context.Context, string) (*novitaboxv1.BalloonStats, error) {
	return nil, errors.New("balloon is not supported by this runtime")
}

func (d *GVisorDriver) UpdateBalloonStats(context.Context, string, uint32) (*novitaboxv1.BalloonConfig, error) {
	return nil, errors.New("balloon is not supported by this runtime")
}

func (d *GVisorDriver) StartBalloonHinting(context.Context, string, bool) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, errors.New("balloon hinting is not supported by this runtime")
}

func (d *GVisorDriver) StopBalloonHinting(context.Context, string) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, errors.New("balloon hinting is not supported by this runtime")
}

func (d *GVisorDriver) GetBalloonHinting(context.Context, string) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, errors.New("balloon hinting is not supported by this runtime")
}

func (d *GVisorDriver) start(ctx context.Context, spec *novitaboxv1.RuntimeSpec, action string) (*novitaboxv1.RuntimeInfo, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}
	spec.RuntimeType = novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER
	if err := validateGVisorSpec(d.cfg, spec); err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.info != nil && !d.stopped && d.info.GetState() == novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING {
		return nil, fmt.Errorf("runtime for sandbox %q is already running", d.info.GetSandboxId())
	}

	paths := d.paths(spec.GetSandboxId())
	_ = unmountUnder(paths.sandboxDir)
	if err := os.RemoveAll(paths.bundleDir); err != nil {
		return nil, fmt.Errorf("remove stale gvisor bundle: %w", err)
	}
	if err := os.RemoveAll(paths.stateDir); err != nil {
		return nil, fmt.Errorf("remove stale gvisor state dir: %w", err)
	}
	if err := os.MkdirAll(paths.bundleDir, 0o755); err != nil {
		return nil, fmt.Errorf("create gvisor bundle: %w", err)
	}
	if err := os.MkdirAll(paths.stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create gvisor state dir: %w", err)
	}
	if err := writeGVisorBundleConfig(d.cfg, spec, filepath.Join(paths.bundleDir, "config.json")); err != nil {
		return nil, err
	}

	_ = d.runsc(ctx, paths, false, "delete", "--force", spec.GetSandboxId())
	gpuEnabled := spec.GetMachine().GetGpu() > 0
	if err := d.runsc(ctx, paths, gpuEnabled, "create", "--bundle", paths.bundleDir, spec.GetSandboxId()); err != nil {
		_ = d.runsc(context.Background(), paths, false, "delete", "--force", spec.GetSandboxId())
		_ = unmountUnder(paths.sandboxDir)
		return nil, fmt.Errorf("create gvisor container: %w", err)
	}
	if err := d.runsc(ctx, paths, gpuEnabled, "start", spec.GetSandboxId()); err != nil {
		_ = d.runsc(context.Background(), paths, false, "delete", "--force", spec.GetSandboxId())
		_ = unmountUnder(paths.sandboxDir)
		return nil, fmt.Errorf("start gvisor container: %w", err)
	}

	info := &novitaboxv1.RuntimeInfo{
		SandboxId:         spec.GetSandboxId(),
		RuntimeType:       novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER,
		State:             novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING,
		Pid:               d.containerPID(ctx, paths, spec.GetSandboxId()),
		ShimSocketPath:    d.cfg.Boxshim.SocketPath,
		RuntimeSocketPath: paths.stateDir,
	}
	d.info = info
	d.spec = spec
	d.stopped = false

	d.logger.Info("started gvisor runtime",
		"sandbox_id", spec.GetSandboxId(),
		"action", action,
		"pid", info.GetPid(),
		"rootfs", spec.GetRootfs().GetPath(),
	)
	return cloneRuntimeInfo(info), nil
}

func (d *GVisorDriver) stopLocked(ctx context.Context, sandboxID string, timeout time.Duration, force bool) error {
	paths := d.paths(sandboxID)
	defer func() {
		_ = unmountUnder(paths.sandboxDir)
	}()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if !force {
		_ = d.runsc(ctx, paths, false, "kill", sandboxID, "TERM")
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if d.containerPID(ctx, paths, sandboxID) == 0 {
				_ = d.runsc(ctx, paths, false, "delete", "--force", sandboxID)
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	_ = d.runsc(ctx, paths, false, "kill", sandboxID, "KILL")
	if err := d.runsc(ctx, paths, false, "delete", "--force", sandboxID); err != nil {
		return fmt.Errorf("delete gvisor container: %w", err)
	}
	return nil
}

func (d *GVisorDriver) checkSandboxLocked(sandboxID string) error {
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

type gvisorPaths struct {
	sandboxDir string
	stateDir   string
	bundleDir  string
	logPath    string
}

func (d *GVisorDriver) paths(sandboxID string) gvisorPaths {
	sandboxDir := layout.New(d.cfg.RootDir).SandboxDir(sandboxID)
	return gvisorPaths{
		sandboxDir: sandboxDir,
		stateDir:   filepath.Join(sandboxDir, gvisorStateDirName),
		bundleDir:  filepath.Join(sandboxDir, gvisorBundleName),
		logPath:    filepath.Join(sandboxDir, gvisorLogName),
	}
}

func (d *GVisorDriver) runsc(ctx context.Context, paths gvisorPaths, gpu bool, args ...string) error {
	runsc := d.cfg.GVisor.RunscBinaryPath
	if runsc == "" {
		runsc = config.DefaultRunscBinaryPath(d.cfg.RootDir)
	}
	cmdArgs := []string{"--root", paths.stateDir, "--overlay2=none"}
	if gpu {
		cmdArgs = append(cmdArgs, "--nvproxy")
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, runsc, cmdArgs...)
	logFile, err := os.OpenFile(paths.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open gvisor log: %w", err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("runsc %s failed: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (d *GVisorDriver) containerPID(ctx context.Context, paths gvisorPaths, sandboxID string) int64 {
	runsc := d.cfg.GVisor.RunscBinaryPath
	if runsc == "" {
		runsc = config.DefaultRunscBinaryPath(d.cfg.RootDir)
	}
	cmd := exec.CommandContext(ctx, runsc, "--root", paths.stateDir, "state", sandboxID)
	data, err := cmd.Output()
	if err != nil {
		return 0
	}
	var state struct {
		Pid int64 `json:"pid"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return 0
	}
	return state.Pid
}

func validateGVisorSpec(cfg config.Config, spec *novitaboxv1.RuntimeSpec) error {
	if spec.GetRootfs() == nil || spec.GetRootfs().GetPath() == "" {
		return errors.New("runtime_spec.rootfs.path is required for gvisor")
	}
	info, err := os.Stat(spec.GetRootfs().GetPath())
	if err != nil {
		return fmt.Errorf("stat gvisor rootfs: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("gvisor rootfs %q must be a directory", spec.GetRootfs().GetPath())
	}
	boxdPath := ContainerPathInRootfs(spec.GetRootfs().GetPath(), cfg.Template.BoxdGuestPath)
	if _, err := os.Stat(boxdPath); err != nil {
		return fmt.Errorf("stat gvisor boxd binary %q: %w", boxdPath, err)
	}
	return nil
}

func writeGVisorBundleConfig(cfg config.Config, spec *novitaboxv1.RuntimeSpec, dest string) error {
	ociSpec, err := newGVisorOCISpec(cfg, spec)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(ociSpec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal gvisor OCI spec: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create gvisor bundle dir: %w", err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("write gvisor OCI spec: %w", err)
	}
	return nil
}

type gvisorOCISpec struct {
	OCIVersion string             `json:"ociVersion"`
	Process    gvisorOCIProcess   `json:"process"`
	Root       gvisorOCIRoot      `json:"root"`
	Hostname   string             `json:"hostname,omitempty"`
	Mounts     []gvisorOCIMount   `json:"mounts,omitempty"`
	Hooks      *gvisorOCIHooks    `json:"hooks,omitempty"`
	Linux      gvisorOCILinuxSpec `json:"linux"`
}

type gvisorOCIProcess struct {
	Terminal        bool                  `json:"terminal"`
	User            gvisorOCIUser         `json:"user"`
	Args            []string              `json:"args"`
	Env             []string              `json:"env"`
	Cwd             string                `json:"cwd"`
	Capabilities    gvisorOCICapabilities `json:"capabilities"`
	Rlimits         []gvisorOCIRlimit     `json:"rlimits,omitempty"`
	NoNewPrivileges bool                  `json:"noNewPrivileges"`
}

type gvisorOCIUser struct {
	UID uint32 `json:"uid"`
	GID uint32 `json:"gid"`
}

type gvisorOCICapabilities struct {
	Bounding    []string `json:"bounding"`
	Effective   []string `json:"effective"`
	Inheritable []string `json:"inheritable"`
	Permitted   []string `json:"permitted"`
	Ambient     []string `json:"ambient"`
}

type gvisorOCIRlimit struct {
	Type string `json:"type"`
	Hard uint64 `json:"hard"`
	Soft uint64 `json:"soft"`
}

type gvisorOCIRoot struct {
	Path     string `json:"path"`
	Readonly bool   `json:"readonly"`
}

type gvisorOCIMount struct {
	Destination string   `json:"destination"`
	Type        string   `json:"type"`
	Source      string   `json:"source"`
	Options     []string `json:"options,omitempty"`
}

type gvisorOCILinuxSpec struct {
	Namespaces    []gvisorOCINamespace `json:"namespaces"`
	MaskedPaths   []string             `json:"maskedPaths,omitempty"`
	ReadonlyPaths []string             `json:"readonlyPaths,omitempty"`
	Devices       []gvisorOCIDevice    `json:"devices,omitempty"`
	Resources     gvisorOCIResources   `json:"resources"`
}

type gvisorOCIHooks struct {
	Prestart []gvisorOCIHook `json:"prestart,omitempty"`
}

type gvisorOCIHook struct {
	Path    string   `json:"path"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	Timeout *uint32  `json:"timeout,omitempty"`
}

type gvisorOCINamespace struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
}

type gvisorOCIResources struct {
	Memory  *gvisorOCIMemory          `json:"memory,omitempty"`
	CPU     *gvisorOCICPU             `json:"cpu,omitempty"`
	Pids    *gvisorOCIPids            `json:"pids,omitempty"`
	Devices []gvisorOCIResourceDevice `json:"devices,omitempty"`
}

type gvisorOCIResourceDevice struct {
	Allow  bool   `json:"allow"`
	Type   string `json:"type,omitempty"`
	Major  *int64 `json:"major,omitempty"`
	Minor  *int64 `json:"minor,omitempty"`
	Access string `json:"access,omitempty"`
}

type gvisorOCIDevice struct {
	Path     string `json:"path"`
	Type     string `json:"type"`
	Major    int64  `json:"major"`
	Minor    int64  `json:"minor"`
	FileMode uint32 `json:"fileMode,omitempty"`
	UID      uint32 `json:"uid"`
	GID      uint32 `json:"gid"`
}

type gvisorOCIMemory struct {
	Limit *int64 `json:"limit,omitempty"`
}

type gvisorOCICPU struct {
	Shares *uint64 `json:"shares,omitempty"`
	Quota  *int64  `json:"quota,omitempty"`
	Period *uint64 `json:"period,omitempty"`
}

type gvisorOCIPids struct {
	Limit int64 `json:"limit"`
}

func newGVisorOCISpec(cfg config.Config, spec *novitaboxv1.RuntimeSpec) (gvisorOCISpec, error) {
	boxdPath := cfg.Template.BoxdGuestPath
	if boxdPath == "" {
		boxdPath = "/novitabox/agent/boxd"
	}
	boxdRoot := path.Dir(path.Dir(boxdPath))
	if boxdRoot == "." || boxdRoot == "/" {
		boxdRoot = "/novitabox"
	}
	boxdAddr := cfg.Template.BoxdGuestAddr
	if boxdAddr == "" {
		boxdAddr = "0.0.0.0:49983"
	}
	caps := defaultContainerCapabilities()
	period := uint64(100000)
	quota := int64(spec.GetMachine().GetVcpu()) * int64(period)
	if quota == 0 {
		quota = int64(period)
	}
	memoryLimit := int64(spec.GetMachine().GetMemoryMb()) * 1024 * 1024
	if memoryLimit == 0 {
		memoryLimit = 512 * 1024 * 1024
	}
	namespaces := []gvisorOCINamespace{
		{Type: "pid"},
		{Type: "ipc"},
		{Type: "uts"},
		{Type: "mount"},
	}
	if spec.GetNetwork().GetNamespaceName() != "" {
		namespaces = append(namespaces, gvisorOCINamespace{
			Type: "network",
			Path: filepath.Join(gvisorNetNSDir, spec.GetNetwork().GetNamespaceName()),
		})
	}
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"TERM=xterm-256color",
	}
	gpuCount := spec.GetMachine().GetGpu()
	cdiEdits, cdiFound, err := loadNvidiaCDIEdits(gpuCount)
	if err != nil {
		return gvisorOCISpec{}, err
	}
	var devices []gvisorOCIDevice
	var deviceRules []gvisorOCIResourceDevice
	if cdiFound {
		env = append(env, cdiEdits.env...)
		devices = append(devices, cdiEdits.devices...)
		deviceRules = append(deviceRules, cdiEdits.rules...)
		if len(devices) == 0 && gpuCount > 0 {
			devices, deviceRules, err = nvidiaDeviceSpecs(gpuCount)
			if err != nil {
				return gvisorOCISpec{}, err
			}
		}
	} else if gpuCount > 0 {
		gpuEnv := nvidiaVisibleDevicesEnv(gpuCount)
		env = append(env,
			"NVIDIA_DRIVER_CAPABILITIES=compute,utility",
			"CUDA_VISIBLE_DEVICES="+gpuEnv,
			"NVIDIA_VISIBLE_DEVICES="+gpuEnv,
		)
		devices, deviceRules, err = nvidiaDeviceSpecs(gpuCount)
		if err != nil {
			return gvisorOCISpec{}, err
		}
	}
	if gpuCount > 0 {
		gpuEnv := nvidiaVisibleDevicesEnv(gpuCount)
		env = setEnv(env,
			"NVIDIA_DRIVER_CAPABILITIES=compute,utility",
			"CUDA_VISIBLE_DEVICES="+gpuEnv,
			"NVIDIA_VISIBLE_DEVICES="+gpuEnv,
			"LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu:/usr/lib/x86_64-linux-gnu/vdpau:/usr/lib64",
		)
	}
	mounts := []gvisorOCIMount{
		{Destination: "/proc", Type: "proc", Source: "proc", Options: []string{"nosuid", "noexec", "nodev"}},
		{Destination: "/dev", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
		{Destination: "/dev/pts", Type: "devpts", Source: "devpts", Options: []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5"}},
		{Destination: "/dev/shm", Type: "tmpfs", Source: "shm", Options: []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"}},
		{Destination: "/sys", Type: "sysfs", Source: "sysfs", Options: []string{"nosuid", "noexec", "nodev", "ro"}},
	}
	mounts = append(mounts, cdiEdits.mounts...)
	ociHooks := cdiEdits.hooks

	return gvisorOCISpec{
		OCIVersion: "1.0.2",
		Process: gvisorOCIProcess{
			Terminal:     false,
			User:         gvisorOCIUser{UID: 0, GID: 0},
			Args:         []string{boxdPath, "--root", boxdRoot, "--addr", boxdAddr},
			Env:          env,
			Cwd:          "/",
			Capabilities: caps,
			Rlimits: []gvisorOCIRlimit{
				{Type: "RLIMIT_NOFILE", Hard: 4096, Soft: 4096},
			},
			NoNewPrivileges: false,
		},
		Root:     gvisorOCIRoot{Path: spec.GetRootfs().GetPath(), Readonly: spec.GetRootfs().GetReadonly()},
		Hostname: spec.GetSandboxId(),
		Mounts:   mounts,
		Hooks:    ociHooks,
		Linux: gvisorOCILinuxSpec{
			Namespaces: namespaces,
			Devices:    devices,
			MaskedPaths: []string{
				"/proc/acpi",
				"/proc/kcore",
				"/proc/keys",
				"/proc/latency_stats",
				"/proc/timer_list",
				"/proc/timer_stats",
				"/proc/sched_debug",
				"/sys/firmware",
			},
			ReadonlyPaths: []string{
				"/proc/asound",
				"/proc/bus",
				"/proc/fs",
				"/proc/irq",
				"/proc/sys",
				"/proc/sysrq-trigger",
			},
			Resources: gvisorOCIResources{
				Memory:  &gvisorOCIMemory{Limit: &memoryLimit},
				CPU:     &gvisorOCICPU{Quota: &quota, Period: &period},
				Pids:    &gvisorOCIPids{Limit: 512},
				Devices: deviceRules,
			},
		},
	}, nil
}

func defaultContainerCapabilities() gvisorOCICapabilities {
	caps := []string{
		"CAP_AUDIT_WRITE",
		"CAP_CHOWN",
		"CAP_DAC_OVERRIDE",
		"CAP_FOWNER",
		"CAP_FSETID",
		"CAP_KILL",
		"CAP_MKNOD",
		"CAP_NET_BIND_SERVICE",
		"CAP_NET_RAW",
		"CAP_SETFCAP",
		"CAP_SETGID",
		"CAP_SETPCAP",
		"CAP_SETUID",
		"CAP_SYS_CHROOT",
	}
	return gvisorOCICapabilities{
		Bounding:    caps,
		Effective:   caps,
		Inheritable: []string{},
		Permitted:   caps,
		Ambient:     []string{},
	}
}

type nvidiaDeviceStat struct {
	major int64
	minor int64
	mode  uint32
	uid   uint32
	gid   uint32
}

var statNvidiaDevice = readNvidiaDeviceStat

func nvidiaDeviceSpecs(gpuCount uint32) ([]gvisorOCIDevice, []gvisorOCIResourceDevice, error) {
	if gpuCount == 0 {
		return nil, nil, nil
	}

	paths := []string{"/dev/nvidiactl", "/dev/nvidia-uvm"}
	for i := uint32(0); i < gpuCount; i++ {
		paths = append(paths, "/dev/nvidia"+strconv.FormatUint(uint64(i), 10))
	}

	devices := make([]gvisorOCIDevice, 0, len(paths))
	rules := make([]gvisorOCIResourceDevice, 0, len(paths))
	for _, devicePath := range paths {
		st, err := statNvidiaDevice(devicePath)
		if err != nil {
			return nil, nil, fmt.Errorf("stat nvidia device %q: %w", devicePath, err)
		}
		major := st.major
		minor := st.minor
		devices = append(devices, gvisorOCIDevice{
			Path:     devicePath,
			Type:     "c",
			Major:    major,
			Minor:    minor,
			FileMode: st.mode,
			UID:      st.uid,
			GID:      st.gid,
		})
		rules = append(rules, gvisorOCIResourceDevice{
			Allow:  true,
			Type:   "c",
			Major:  &major,
			Minor:  &minor,
			Access: "rwm",
		})
	}

	return devices, rules, nil
}

func nvidiaVisibleDevicesEnv(gpuCount uint32) string {
	if gpuCount == 0 {
		return ""
	}
	values := make([]string, 0, gpuCount)
	for i := uint32(0); i < gpuCount; i++ {
		values = append(values, strconv.FormatUint(uint64(i), 10))
	}
	return strings.Join(values, ",")
}

type cdiNvidiaEdits struct {
	env     []string
	mounts  []gvisorOCIMount
	hooks   *gvisorOCIHooks
	devices []gvisorOCIDevice
	rules   []gvisorOCIResourceDevice
}

type cdiSpecFile struct {
	ContainerEdits *cdiContainerEdits `json:"containerEdits" yaml:"containerEdits"`
	Devices        []cdiDevice        `json:"devices" yaml:"devices"`
}

type cdiDevice struct {
	Name           string             `json:"name" yaml:"name"`
	ContainerEdits *cdiContainerEdits `json:"containerEdits" yaml:"containerEdits"`
}

type cdiContainerEdits struct {
	DeviceNodes []cdiDeviceNode `json:"deviceNodes" yaml:"deviceNodes"`
	Env         []string        `json:"env" yaml:"env"`
	Mounts      []cdiMount      `json:"mounts" yaml:"mounts"`
	Hooks       []cdiHook       `json:"hooks" yaml:"hooks"`
}

type cdiDeviceNode struct {
	Path     string  `json:"path" yaml:"path"`
	Type     string  `json:"type" yaml:"type"`
	Major    *int64  `json:"major,omitempty" yaml:"major,omitempty"`
	Minor    *int64  `json:"minor,omitempty" yaml:"minor,omitempty"`
	FileMode *uint32 `json:"fileMode,omitempty" yaml:"fileMode,omitempty"`
	UID      *uint32 `json:"uid,omitempty" yaml:"uid,omitempty"`
	GID      *uint32 `json:"gid,omitempty" yaml:"gid,omitempty"`
}

type cdiMount struct {
	HostPath      string   `json:"hostPath" yaml:"hostPath"`
	ContainerPath string   `json:"containerPath" yaml:"containerPath"`
	Options       []string `json:"options,omitempty" yaml:"options,omitempty"`
}

type cdiHook struct {
	HookName string   `json:"hookName" yaml:"hookName"`
	Path     string   `json:"path" yaml:"path"`
	Args     []string `json:"args,omitempty" yaml:"args,omitempty"`
	Env      []string `json:"env,omitempty" yaml:"env,omitempty"`
	Timeout  *uint32  `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

func loadNvidiaCDIEdits(gpuCount uint32) (cdiNvidiaEdits, bool, error) {
	if gpuCount == 0 {
		return cdiNvidiaEdits{}, false, nil
	}
	spec, err := findNvidiaCDISpec()
	if err != nil {
		return cdiNvidiaEdits{}, false, err
	}
	if spec == "" {
		return cdiNvidiaEdits{}, false, nil
	}

	parsed, err := readCDISpec(spec)
	if err != nil {
		return cdiNvidiaEdits{}, false, err
	}

	out := cdiNvidiaEdits{}
	mergeCDIContainerEdits(&out, parsed.ContainerEdits)
	selected := false
	for i := uint32(0); i < gpuCount; i++ {
		name := fmt.Sprintf("nvidia.com/gpu=%d", i)
		if edit := findCDIDeviceEdit(parsed.Devices, name); edit != nil {
			mergeCDIContainerEdits(&out, edit)
			selected = true
		}
	}
	if edit := findCDIDeviceEdit(parsed.Devices, "nvidia.com/gpu=all"); edit != nil {
		mergeCDIContainerEdits(&out, edit)
		selected = true
	}
	if !selected && len(parsed.Devices) > 0 {
		limit := int(gpuCount)
		if limit > len(parsed.Devices) {
			limit = len(parsed.Devices)
		}
		for i := 0; i < limit; i++ {
			mergeCDIContainerEdits(&out, parsed.Devices[i].ContainerEdits)
		}
	}
	return out, true, nil
}

func findNvidiaCDISpec() (string, error) {
	candidates := []string{
		"/etc/cdi/nvidia.yaml",
		"/etc/cdi/nvidia.yml",
		"/etc/cdi/nvidia.json",
		"/var/run/cdi/nvidia.yaml",
		"/var/run/cdi/nvidia.yml",
		"/var/run/cdi/nvidia.json",
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}

func readCDISpec(path string) (*cdiSpecFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec cdiSpecFile
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse CDI spec %q: %w", path, err)
	}
	return &spec, nil
}

func findCDIDeviceEdit(devices []cdiDevice, name string) *cdiContainerEdits {
	for i := range devices {
		if devices[i].Name == name {
			return devices[i].ContainerEdits
		}
	}
	return nil
}

func mergeCDIContainerEdits(dst *cdiNvidiaEdits, edits *cdiContainerEdits) {
	if edits == nil {
		return
	}
	dst.env = append(dst.env, edits.Env...)
	for _, node := range edits.DeviceNodes {
		if node.Path == "" || node.Type == "" {
			continue
		}
		nodeStat, err := readDeviceNodeStat(node.Path)
		if err != nil {
			nodeStat = nil
		}
		device := gvisorOCIDevice{
			Path: node.Path,
			Type: node.Type,
			UID:  derefUint32(node.UID),
			GID:  derefUint32(node.GID),
		}
		if nodeStat != nil {
			device.UID = nodeStat.uid
			device.GID = nodeStat.gid
		}
		if node.FileMode != nil {
			device.FileMode = *node.FileMode
		} else if nodeStat != nil {
			device.FileMode = nodeStat.mode
		}
		if node.Major != nil {
			device.Major = *node.Major
		} else if nodeStat != nil {
			device.Major = nodeStat.major
		}
		if node.Minor != nil {
			device.Minor = *node.Minor
		} else if nodeStat != nil {
			device.Minor = nodeStat.minor
		}
		major := node.Major
		minor := node.Minor
		if nodeStat != nil {
			major = &device.Major
			minor = &device.Minor
		}
		dst.devices = append(dst.devices, device)
		dst.rules = append(dst.rules, gvisorOCIResourceDevice{
			Allow:  true,
			Type:   node.Type,
			Major:  major,
			Minor:  minor,
			Access: "rwm",
		})
	}
	for _, mount := range edits.Mounts {
		if mount.HostPath == "" || mount.ContainerPath == "" {
			continue
		}
		dst.mounts = append(dst.mounts, gvisorOCIMount{
			Source:      mount.HostPath,
			Destination: mount.ContainerPath,
			Type:        "bind",
			Options:     append([]string{}, mount.Options...),
		})
	}
	appendNvidiaLibrarySymlinkHooks(dst)
	if len(edits.Hooks) > 0 {
		if dst.hooks == nil {
			dst.hooks = &gvisorOCIHooks{}
		}
		for _, hook := range edits.Hooks {
			if hook.HookName != "" && hook.HookName != "createContainer" {
				continue
			}
			if len(hook.Args) > 1 && hook.Args[1] == "update-ldcache" {
				continue
			}
			dst.hooks.Prestart = append(dst.hooks.Prestart, gvisorOCIHook{
				Path:    hook.Path,
				Args:    append([]string{}, hook.Args...),
				Env:     append([]string{}, hook.Env...),
				Timeout: hook.Timeout,
			})
		}
	}
}

func appendNvidiaLibrarySymlinkHooks(dst *cdiNvidiaEdits) {
	links := nvidiaLibrarySymlinks(dst.mounts)
	if len(links) == 0 {
		return
	}
	if dst.hooks == nil {
		dst.hooks = &gvisorOCIHooks{}
	}
	args := []string{"nvidia-cdi-hook", "create-symlinks"}
	for _, link := range links {
		if hasNvidiaSymlinkHook(dst.hooks.Prestart, link) {
			continue
		}
		args = append(args, "--link", link)
	}
	if len(args) == 2 {
		return
	}
	dst.hooks.Prestart = append(dst.hooks.Prestart, gvisorOCIHook{
		Path: "/usr/bin/nvidia-cdi-hook",
		Args: args,
		Env:  []string{"NVIDIA_CTK_DEBUG=false"},
	})
}

func nvidiaLibrarySymlinks(mounts []gvisorOCIMount) []string {
	var out []string
	for _, mount := range mounts {
		dir, file := path.Split(mount.Destination)
		switch {
		case strings.HasPrefix(file, "libcuda.so.") && file != "libcuda.so.1":
			out = append(out, file+"::"+path.Join(dir, "libcuda.so.1"))
		case strings.HasPrefix(file, "libnvidia-ml.so.") && file != "libnvidia-ml.so.1":
			out = append(out,
				file+"::"+path.Join(dir, "libnvidia-ml.so.1"),
				"libnvidia-ml.so.1::"+path.Join(dir, "libnvidia-ml.so"),
			)
		}
	}
	return out
}

func hasNvidiaSymlinkHook(hooks []gvisorOCIHook, link string) bool {
	for _, hook := range hooks {
		if len(hook.Args) < 2 || hook.Args[1] != "create-symlinks" {
			continue
		}
		for _, arg := range hook.Args {
			if arg == link {
				return true
			}
		}
	}
	return false
}

func derefUint32(v *uint32) uint32 {
	if v == nil {
		return 0
	}
	return *v
}

func setEnv(env []string, values ...string) []string {
	existing := make(map[string]struct{}, len(env))
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		existing[key] = struct{}{}
	}
	out := make([]string, 0, len(env)+len(values))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, replace := existing[key]; replace {
				continue
			}
		}
		out = append(out, item)
	}
	for _, value := range values {
		if _, _, ok := strings.Cut(value, "="); ok {
			out = append(out, value)
		}
	}
	return out
}

type deviceNodeStat struct {
	major int64
	minor int64
	mode  uint32
	uid   uint32
	gid   uint32
}

func readDeviceNodeStat(path string) (*deviceNodeStat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("unsupported stat type %T", info.Sys())
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return nil, fmt.Errorf("%s is not a device node", path)
	}
	return &deviceNodeStat{
		major: deviceMajor(uint64(stat.Rdev)),
		minor: deviceMinor(uint64(stat.Rdev)),
		mode:  uint32(info.Mode().Perm()),
		uid:   stat.Uid,
		gid:   stat.Gid,
	}, nil
}

func readNvidiaDeviceStat(path string) (nvidiaDeviceStat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nvidiaDeviceStat{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nvidiaDeviceStat{}, fmt.Errorf("unsupported stat type %T", info.Sys())
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return nvidiaDeviceStat{}, fmt.Errorf("%s is not a device node", path)
	}
	return nvidiaDeviceStat{
		major: deviceMajor(uint64(stat.Rdev)),
		minor: deviceMinor(uint64(stat.Rdev)),
		mode:  uint32(info.Mode().Perm()),
		uid:   stat.Uid,
		gid:   stat.Gid,
	}, nil
}

func deviceMajor(dev uint64) int64 {
	return int64(((dev >> 8) & 0xfff) | ((dev >> 32) & 0xfffff000))
}

func deviceMinor(dev uint64) int64 {
	return int64((dev & 0xff) | ((dev >> 12) & 0xffffff00))
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
