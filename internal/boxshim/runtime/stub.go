package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
)

type StubDriver struct {
	cfg    config.Config
	logger *log.Logger

	mu   sync.Mutex
	info *novitaboxv1.RuntimeInfo
	spec *novitaboxv1.RuntimeSpec
}

func NewStubDriver(cfg config.Config, logger *log.Logger) *StubDriver {
	return &StubDriver{
		cfg:    cfg,
		logger: logger,
	}
}

func (d *StubDriver) Create(_ context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	info := d.newRuntimeInfo(spec, novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING)
	d.spec = spec
	d.info = info

	d.logger.Info("created stub runtime",
		"sandbox_id", spec.GetSandboxId(),
		"runtime_type", spec.GetRuntimeType().String(),
	)

	return cloneRuntimeInfo(info), nil
}

func (d *StubDriver) Pause(_ context.Context, sandboxID string) (*novitaboxv1.RuntimeInfo, error) {
	return d.transition(sandboxID, "pause", novitaboxv1.RuntimeState_RUNTIME_STATE_PAUSED)
}

func (d *StubDriver) Resume(_ context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.info == nil {
		info := d.newRuntimeInfo(spec, novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING)
		d.spec = spec
		d.info = info
		return cloneRuntimeInfo(info), nil
	}
	if d.info.GetSandboxId() != spec.GetSandboxId() {
		return nil, fmt.Errorf("runtime sandbox mismatch: have %q, got %q", d.info.GetSandboxId(), spec.GetSandboxId())
	}

	d.spec = spec
	d.info.RuntimeType = spec.GetRuntimeType()
	d.info.State = novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING
	d.info.ErrorMessage = ""

	return cloneRuntimeInfo(d.info), nil
}

func (d *StubDriver) Kill(_ context.Context, sandboxID string) error {
	_, err := d.transition(sandboxID, "kill", novitaboxv1.RuntimeState_RUNTIME_STATE_EXITED)
	return err
}

func (d *StubDriver) Start(_ context.Context, spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeInfo, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.info == nil {
		info := d.newRuntimeInfo(spec, novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING)
		d.spec = spec
		d.info = info
		return cloneRuntimeInfo(info), nil
	}
	if d.info.GetSandboxId() != spec.GetSandboxId() {
		return nil, fmt.Errorf("runtime sandbox mismatch: have %q, got %q", d.info.GetSandboxId(), spec.GetSandboxId())
	}

	d.spec = spec
	d.info.RuntimeType = spec.GetRuntimeType()
	d.info.State = novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING
	d.info.ErrorMessage = ""

	return cloneRuntimeInfo(d.info), nil
}

func (d *StubDriver) Stop(_ context.Context, sandboxID string, _ time.Duration) (*novitaboxv1.RuntimeInfo, error) {
	return d.transition(sandboxID, "stop", novitaboxv1.RuntimeState_RUNTIME_STATE_STOPPED)
}

func (d *StubDriver) Reboot(_ context.Context, sandboxID string, _ time.Duration) (*novitaboxv1.RuntimeInfo, error) {
	return d.transition(sandboxID, "reboot", novitaboxv1.RuntimeState_RUNTIME_STATE_RUNNING)
}

func (d *StubDriver) Status(_ context.Context, sandboxID string) (*novitaboxv1.RuntimeInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.info == nil {
		return &novitaboxv1.RuntimeInfo{SandboxId: sandboxID, State: novitaboxv1.RuntimeState_RUNTIME_STATE_UNKNOWN}, nil
	}
	if sandboxID != "" && d.info.GetSandboxId() != sandboxID {
		return nil, fmt.Errorf("runtime sandbox mismatch: have %q, got %q", d.info.GetSandboxId(), sandboxID)
	}

	return cloneRuntimeInfo(d.info), nil
}

func (d *StubDriver) Capabilities(_ context.Context, runtimeType novitaboxv1.RuntimeType) (*novitaboxv1.RuntimeCapabilities, error) {
	if runtimeType == novitaboxv1.RuntimeType_RUNTIME_TYPE_UNSPECIFIED && d.spec != nil {
		runtimeType = d.spec.GetRuntimeType()
	}
	return defaultCapabilities(runtimeType), nil
}

func (d *StubDriver) UpdateBalloon(context.Context, string, uint32) (*novitaboxv1.BalloonConfig, error) {
	return nil, errors.New("balloon is not supported by this runtime")
}

func (d *StubDriver) GetBalloon(context.Context, string) (*novitaboxv1.BalloonConfig, error) {
	return nil, errors.New("balloon is not supported by this runtime")
}

func (d *StubDriver) GetBalloonStats(context.Context, string) (*novitaboxv1.BalloonStats, error) {
	return nil, errors.New("balloon is not supported by this runtime")
}

func (d *StubDriver) UpdateBalloonStats(context.Context, string, uint32) (*novitaboxv1.BalloonConfig, error) {
	return nil, errors.New("balloon is not supported by this runtime")
}

func (d *StubDriver) StartBalloonHinting(context.Context, string, bool) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, errors.New("balloon hinting is not supported by this runtime")
}

func (d *StubDriver) StopBalloonHinting(context.Context, string) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, errors.New("balloon hinting is not supported by this runtime")
}

func (d *StubDriver) GetBalloonHinting(context.Context, string) (*novitaboxv1.BalloonHintingStatus, error) {
	return nil, errors.New("balloon hinting is not supported by this runtime")
}

func (d *StubDriver) transition(sandboxID string, action string, state novitaboxv1.RuntimeState) (*novitaboxv1.RuntimeInfo, error) {
	if sandboxID == "" {
		return nil, errors.New("sandbox_id is required")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.info == nil {
		return nil, fmt.Errorf("runtime for sandbox %q is not created", sandboxID)
	}
	if d.info.GetSandboxId() != sandboxID {
		return nil, fmt.Errorf("runtime sandbox mismatch: have %q, got %q", d.info.GetSandboxId(), sandboxID)
	}

	d.info.State = state
	d.info.ErrorMessage = ""
	d.logger.Info("updated stub runtime state",
		"sandbox_id", sandboxID,
		"action", action,
		"state", state.String(),
	)

	return cloneRuntimeInfo(d.info), nil
}

func (d *StubDriver) newRuntimeInfo(spec *novitaboxv1.RuntimeSpec, state novitaboxv1.RuntimeState) *novitaboxv1.RuntimeInfo {
	sandboxDir := layout.New(d.cfg.RootDir).SandboxDir(spec.GetSandboxId())
	return &novitaboxv1.RuntimeInfo{
		SandboxId:         spec.GetSandboxId(),
		RuntimeType:       spec.GetRuntimeType(),
		State:             state,
		Pid:               int64(os.Getpid()),
		ShimSocketPath:    d.cfg.Boxshim.SocketPath,
		RuntimeSocketPath: filepath.Join(sandboxDir, "runtime.sock"),
	}
}

func normalizeSpec(spec *novitaboxv1.RuntimeSpec) (*novitaboxv1.RuntimeSpec, error) {
	if spec == nil {
		return nil, errors.New("runtime_spec is required")
	}
	if spec.GetSandboxId() == "" {
		return nil, errors.New("runtime_spec.sandbox_id is required")
	}

	cloned := *spec
	if cloned.RuntimeType == novitaboxv1.RuntimeType_RUNTIME_TYPE_UNSPECIFIED {
		cloned.RuntimeType = novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER
	}

	return &cloned, nil
}

func cloneRuntimeInfo(info *novitaboxv1.RuntimeInfo) *novitaboxv1.RuntimeInfo {
	if info == nil {
		return nil
	}
	cloned := *info
	return &cloned
}

func defaultCapabilities(runtimeType novitaboxv1.RuntimeType) *novitaboxv1.RuntimeCapabilities {
	caps := &novitaboxv1.RuntimeCapabilities{
		StartFromImage:    true,
		StartFromTemplate: true,
		StartFromSnapshot: true,
		Pause:             true,
		Resume:            true,
		FullSnapshot:      true,
		Vsock:             true,
		TapNetwork:        true,
		GracefulShutdown:  true,
		SerialConsole:     true,
		Jailer:            true,
	}

	switch runtimeType {
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR:
		caps.Gpu = true
		caps.HotplugDisk = true
		caps.HotplugNetwork = true
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER:
		caps.StartFromSnapshot = false
		caps.Pause = false
		caps.Resume = false
		caps.FullSnapshot = false
		caps.Vsock = false
		caps.SerialConsole = false
		caps.Jailer = false
	}

	return caps
}
