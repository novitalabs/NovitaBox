package overlaybd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type CtrPullerConfig struct {
	BinaryPath        string
	ContainerdAddress string
	Namespace         string
	Snapshotter       string
}

type CommandRunner interface {
	Run(ctx context.Context, path string, args ...string) ([]byte, error)
}

type CtrPuller struct {
	cfg    CtrPullerConfig
	runner CommandRunner
}

func NewCtrPuller(cfg CtrPullerConfig, runner CommandRunner) *CtrPuller {
	if runner == nil {
		runner = execCommandRunner{}
	}
	return &CtrPuller{cfg: cfg, runner: runner}
}

func (p *CtrPuller) Pull(ctx context.Context, sourceRef string) error {
	args := []string{
		"--address", p.cfg.ContainerdAddress,
		"--namespace", p.cfg.Namespace,
		"rpull", "--snapshotter", p.cfg.Snapshotter,
		sourceRef,
	}
	output, err := p.runner.Run(ctx, p.cfg.BinaryPath, args...)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("run overlaybd rpull: %w", err)
		}
		return fmt.Errorf("run overlaybd rpull: %w: %s", err, message)
	}
	return nil
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, path string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}
