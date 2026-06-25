package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/novitalabs/NovitaBox/internal/app/boxshim"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

func main() {
	defaults := config.Default()
	opts := config.BoxshimOptions{
		RootDir:       defaults.RootDir,
		SocketPath:    defaults.Boxshim.SocketPath,
		RuntimeDriver: defaults.Boxshim.RuntimeDriver,
	}

	cmd := &cobra.Command{
		Use:   "boxshim",
		Short: "Run the NovitaBox per-sandbox runtime shim",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(config.ApplyBoxshimOptions(defaults, opts))
		},
	}
	cmd.Flags().StringVar(&opts.RootDir, "root", opts.RootDir, "NovitaBox root directory")
	cmd.Flags().StringVar(&opts.SocketPath, "socket", opts.SocketPath, "boxshim Unix socket path")
	cmd.Flags().StringVar(&opts.RuntimeDriver, "runtime-driver", opts.RuntimeDriver, "runtime driver: stub or firecracker")
	cmd.Flags().StringVar(&opts.FirecrackerBinaryPath, "firecracker-bin", opts.FirecrackerBinaryPath, "Firecracker binary path, defaults to $root/firecracker")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "boxshim: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := boxshim.New(cfg, log.New(os.Stdout))
	if err != nil {
		return fmt.Errorf("create boxshim app: %w", err)
	}

	if err := app.Run(ctx); err != nil {
		return fmt.Errorf("run boxshim app: %w", err)
	}

	return nil
}
