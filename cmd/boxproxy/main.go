package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/novitalabs/NovitaBox/internal/app/boxproxy"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

func main() {
	defaults := config.Default()
	opts := config.ServiceOptions{
		RootDir: defaults.RootDir,
		Addr:    defaults.BoxProxy.Addr,
	}

	cmd := &cobra.Command{
		Use:   "boxproxy",
		Short: "Run the NovitaBox data-plane proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.ApplyServiceOptions(defaults, "boxproxy", opts)
			if err != nil {
				return err
			}

			return run(cfg)
		},
	}
	cmd.Flags().StringVar(&opts.RootDir, "root", opts.RootDir, "NovitaBox root directory")
	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "boxproxy listen address")
	cmd.Flags().IntVar(&opts.Port, "port", 0, "boxproxy listen port, overrides the port in --addr")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "boxproxy: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := boxproxy.New(cfg, log.New(os.Stdout))
	if err != nil {
		return fmt.Errorf("create boxproxy app: %w", err)
	}

	if err := app.Run(ctx); err != nil {
		return fmt.Errorf("run boxproxy app: %w", err)
	}

	return nil
}
