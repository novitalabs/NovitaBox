package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/novitalabs/NovitaBox/internal/app/boxlet"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

func main() {
	defaults := config.Default()
	opts := config.ServiceOptions{
		RootDir:               defaults.RootDir,
		Addr:                  defaults.Boxlet.Addr,
		RuntimeDriver:         defaults.Boxshim.RuntimeDriver,
		RunscBinaryPath:       defaults.GVisor.RunscBinaryPath,
		OverlayBDContainerd:   defaults.OverlayBD.ContainerdAddress,
		OverlayBDNamespace:    defaults.OverlayBD.Namespace,
		OverlayBDSnapshotter:  defaults.OverlayBD.Snapshotter,
		OverlayBDCtrBinary:    defaults.OverlayBD.CtrBinaryPath,
		OverlayBDSocket:       defaults.OverlayBD.SnapshotterSocket,
		TemplateSnapshotWait:  defaults.Template.SnapshotWaitSecs,
		TemplateAgentWait:     defaults.Template.AgentWaitSecs,
		TemplateBoxdGuestPath: defaults.Template.BoxdGuestPath,
		TemplateBoxdGuestAddr: defaults.Template.BoxdGuestAddr,
		NetworkEnabled:        defaults.Network.Enabled,
		NetworkHostAccessCIDR: defaults.Network.HostAccessCIDR,
		NetworkVethCIDR:       defaults.Network.VethCIDR,
		NetworkGuestIP:        defaults.Network.GuestIP,
		NetworkGatewayIP:      defaults.Network.GatewayIP,
		NetworkGuestMAC:       defaults.Network.GuestMAC,
	}

	cmd := &cobra.Command{
		Use:   "boxlet",
		Short: "Run the NovitaBox node agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.ApplyServiceOptions(defaults, "boxlet", opts)
			if err != nil {
				return err
			}

			return run(cfg)
		},
	}
	cmd.Flags().StringVar(&opts.RootDir, "root", opts.RootDir, "NovitaBox root directory")
	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "boxlet listen address")
	cmd.Flags().IntVar(&opts.Port, "port", 0, "boxlet listen port, overrides the port in --addr")
	cmd.Flags().StringVar(&opts.DBPath, "db-path", opts.DBPath, "SQLite database path, defaults to $root/db/novitabox.db")
	cmd.Flags().StringVar(&opts.RuntimeDriver, "runtime-driver", opts.RuntimeDriver, "runtime driver passed to boxshim, defaults to firecracker")
	cmd.Flags().StringVar(&opts.RunscBinaryPath, "runsc-bin", opts.RunscBinaryPath, "runsc binary path used by the gVisor runtime, defaults to $root/runsc")
	cmd.Flags().StringVar(&opts.OverlayBDContainerd, "overlaybd-containerd", opts.OverlayBDContainerd, "containerd socket used by OverlayBD")
	cmd.Flags().StringVar(&opts.OverlayBDNamespace, "overlaybd-namespace", opts.OverlayBDNamespace, "containerd namespace used by OverlayBD")
	cmd.Flags().StringVar(&opts.OverlayBDSnapshotter, "overlaybd-snapshotter", opts.OverlayBDSnapshotter, "containerd OverlayBD snapshotter name")
	cmd.Flags().StringVar(&opts.OverlayBDCtrBinary, "overlaybd-ctr", opts.OverlayBDCtrBinary, "OverlayBD extended ctr binary path")
	cmd.Flags().StringVar(&opts.OverlayBDSocket, "overlaybd-socket", opts.OverlayBDSocket, "OverlayBD snapshotter socket path")
	cmd.Flags().StringVar(&opts.TemplateKernelPath, "template-kernel", opts.TemplateKernelPath, "kernel image used when building template snapshots, defaults to $root/vmlinux.bin")
	cmd.Flags().Uint32Var(&opts.TemplateSnapshotWait, "template-snapshot-wait", opts.TemplateSnapshotWait, "seconds to wait before exporting template snapshot")
	cmd.Flags().StringVar(&opts.TemplateAgentHealth, "template-agent-health", opts.TemplateAgentHealth, "boxd health URL used before exporting template snapshot")
	cmd.Flags().StringVar(&opts.TemplateAgentExec, "template-agent-exec", opts.TemplateAgentExec, "boxd exec URL used for template build commands")
	cmd.Flags().Uint32Var(&opts.TemplateAgentWait, "template-agent-wait", opts.TemplateAgentWait, "seconds to wait for boxd health before exporting template snapshot")
	cmd.Flags().StringVar(&opts.TemplateBoxdBinary, "template-boxd-bin", opts.TemplateBoxdBinary, "host boxd binary path packaged into readonly agent disk, defaults to $root/boxd")
	cmd.Flags().StringVar(&opts.TemplateBoxdGuestPath, "template-boxd-guest-path", opts.TemplateBoxdGuestPath, "guest boxd path, defaults to /novitabox/agent/boxd")
	cmd.Flags().StringVar(&opts.TemplateBoxdGuestAddr, "template-boxd-guest-addr", opts.TemplateBoxdGuestAddr, "guest listen address for boxd")
	cmd.Flags().BoolVar(&opts.NetworkEnabled, "network", opts.NetworkEnabled, "prepare sandbox network namespace and tap network")
	cmd.Flags().StringVar(&opts.NetworkHostAccessCIDR, "network-host-access-cidr", opts.NetworkHostAccessCIDR, "CIDR used to allocate per-sandbox host access IPs")
	cmd.Flags().StringVar(&opts.NetworkVethCIDR, "network-veth-cidr", opts.NetworkVethCIDR, "CIDR used to allocate host/netns veth pairs")
	cmd.Flags().StringVar(&opts.NetworkGuestIP, "network-guest-ip", opts.NetworkGuestIP, "fixed guest IP used inside each sandbox")
	cmd.Flags().StringVar(&opts.NetworkGatewayIP, "network-gateway-ip", opts.NetworkGatewayIP, "fixed gateway IP used inside each sandbox network namespace")
	cmd.Flags().StringVar(&opts.NetworkGuestMAC, "network-guest-mac", opts.NetworkGuestMAC, "fixed guest MAC address used by Firecracker network interface")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "boxlet: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := boxlet.New(cfg, log.New(os.Stdout))
	if err != nil {
		return fmt.Errorf("create boxlet app: %w", err)
	}

	if err := app.Run(ctx); err != nil {
		return fmt.Errorf("run boxlet app: %w", err)
	}

	return nil
}
