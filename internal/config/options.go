package config

import (
	"fmt"
	"net"
	"path/filepath"
)

type ServiceOptions struct {
	RootDir               string
	Addr                  string
	Port                  int
	DBPath                string
	BoxletAddr            string
	RuntimeDriver         string
	BoxshimBinaryPath     string
	FirecrackerBinaryPath string
	TemplateKernelPath    string
	TemplateSnapshotWait  uint32
	TemplateAgentHealth   string
	TemplateAgentExec     string
	TemplateAgentWait     uint32
	TemplateBoxdBinary    string
	TemplateBoxdGuestPath string
	TemplateBoxdGuestAddr string
	NetworkEnabled        bool
	NetworkHostAccessCIDR string
	NetworkVethCIDR       string
	NetworkGuestIP        string
	NetworkGatewayIP      string
	NetworkGuestMAC       string
}

func ApplyServiceOptions(defaults Config, name string, opts ServiceOptions) (Config, error) {
	cfg := defaults
	cfg.RootDir = opts.RootDir
	cfg.Storage.DBPath = opts.DBPath
	if cfg.Storage.DBPath == "" {
		cfg.Storage.DBPath = DefaultDBPath(cfg.RootDir)
	}
	if opts.BoxletAddr != "" {
		cfg.Boxlet.Addr = opts.BoxletAddr
	}
	if opts.RuntimeDriver != "" {
		cfg.Boxshim.RuntimeDriver = opts.RuntimeDriver
	}
	if opts.BoxshimBinaryPath != "" {
		cfg.Boxshim.BinaryPath = opts.BoxshimBinaryPath
	}
	if cfg.Boxshim.BinaryPath == "" {
		cfg.Boxshim.BinaryPath = DefaultBoxshimBinaryPath(cfg.RootDir)
	}
	if opts.FirecrackerBinaryPath != "" {
		cfg.Firecracker.BinaryPath = opts.FirecrackerBinaryPath
	}
	if cfg.Firecracker.BinaryPath == "" {
		cfg.Firecracker.BinaryPath = DefaultFirecrackerBinaryPath(cfg.RootDir)
	}
	if opts.TemplateKernelPath != "" {
		cfg.Template.KernelPath = opts.TemplateKernelPath
	}
	if cfg.Template.KernelPath == "" {
		cfg.Template.KernelPath = DefaultKernelPath(cfg.RootDir)
	}
	if opts.TemplateSnapshotWait > 0 {
		cfg.Template.SnapshotWaitSecs = opts.TemplateSnapshotWait
	}
	if opts.TemplateAgentHealth != "" {
		cfg.Template.AgentHealthURL = opts.TemplateAgentHealth
	}
	if opts.TemplateAgentExec != "" {
		cfg.Template.AgentExecURL = opts.TemplateAgentExec
	}
	if opts.TemplateAgentWait > 0 {
		cfg.Template.AgentWaitSecs = opts.TemplateAgentWait
	}
	if opts.TemplateBoxdBinary != "" {
		cfg.Template.BoxdBinaryPath = opts.TemplateBoxdBinary
	}
	if cfg.Template.BoxdBinaryPath == "" {
		cfg.Template.BoxdBinaryPath = DefaultBoxdBinaryPath(cfg.RootDir)
	}
	if opts.TemplateBoxdGuestPath != "" {
		cfg.Template.BoxdGuestPath = opts.TemplateBoxdGuestPath
	}
	if opts.TemplateBoxdGuestAddr != "" {
		cfg.Template.BoxdGuestAddr = opts.TemplateBoxdGuestAddr
	}
	cfg.Network.Enabled = opts.NetworkEnabled
	if opts.NetworkHostAccessCIDR != "" {
		cfg.Network.HostAccessCIDR = opts.NetworkHostAccessCIDR
	}
	if opts.NetworkVethCIDR != "" {
		cfg.Network.VethCIDR = opts.NetworkVethCIDR
	}
	if opts.NetworkGuestIP != "" {
		cfg.Network.GuestIP = opts.NetworkGuestIP
	}
	if opts.NetworkGatewayIP != "" {
		cfg.Network.GatewayIP = opts.NetworkGatewayIP
	}
	if opts.NetworkGuestMAC != "" {
		cfg.Network.GuestMAC = opts.NetworkGuestMAC
	}

	addr := opts.Addr
	if opts.Port > 0 {
		next, err := withPort(addr, opts.Port)
		if err != nil {
			return Config{}, err
		}
		addr = next
	}

	switch name {
	case "boxapi":
		cfg.BoxAPI.Addr = addr
	case "boxlet":
		cfg.Boxlet.Addr = addr
	case "boxproxy":
		cfg.BoxProxy.Addr = addr
	case "boxd":
		cfg.Boxd.Addr = addr
	default:
		return Config{}, fmt.Errorf("unknown service %q", name)
	}

	return cfg, nil
}

type BoxshimOptions struct {
	RootDir               string
	SocketPath            string
	RuntimeDriver         string
	FirecrackerBinaryPath string
	DBPath                string
}

func ApplyBoxshimOptions(defaults Config, opts BoxshimOptions) Config {
	cfg := defaults
	cfg.RootDir = opts.RootDir
	cfg.Boxshim.SocketPath = opts.SocketPath
	cfg.Boxshim.RuntimeDriver = opts.RuntimeDriver
	cfg.Firecracker.BinaryPath = opts.FirecrackerBinaryPath
	cfg.Storage.DBPath = opts.DBPath
	if cfg.Storage.DBPath == "" {
		cfg.Storage.DBPath = DefaultDBPath(cfg.RootDir)
	}
	if cfg.Firecracker.BinaryPath == "" {
		cfg.Firecracker.BinaryPath = DefaultFirecrackerBinaryPath(cfg.RootDir)
	}

	return cfg
}

func DefaultDBPath(rootDir string) string {
	return filepath.Join(rootDir, "db", "novitabox.db")
}

func DefaultKernelPath(rootDir string) string {
	return filepath.Join(rootDir, "vmlinux.bin")
}

func DefaultBoxdBinaryPath(rootDir string) string {
	return filepath.Join(rootDir, "boxd")
}

func DefaultBoxshimBinaryPath(rootDir string) string {
	return filepath.Join(rootDir, "boxshim")
}

func DefaultFirecrackerBinaryPath(rootDir string) string {
	return filepath.Join(rootDir, "firecracker")
}

func withPort(addr string, port int) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("parse address %q: %w", addr, err)
	}

	return net.JoinHostPort(host, fmt.Sprintf("%d", port)), nil
}
