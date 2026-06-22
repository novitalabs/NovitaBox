package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/novitalabs/NovitaBox/internal/app/boxd"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

func main() {
	defaults := config.Default()
	opts := config.ServiceOptions{
		RootDir: defaults.RootDir,
		Addr:    defaults.Boxd.Addr,
	}

	cmd := &cobra.Command{
		Use:   "boxd",
		Short: "Run the NovitaBox guest agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.ApplyServiceOptions(defaults, "boxd", opts)
			if err != nil {
				return err
			}

			return run(cfg)
		},
	}
	cmd.Flags().StringVar(&opts.RootDir, "root", opts.RootDir, "NovitaBox root directory")
	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "boxd listen address")
	cmd.Flags().IntVar(&opts.Port, "port", 0, "boxd listen port, overrides the port in --addr")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "boxd: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := boxd.New(cfg, log.New(os.Stdout))
	if err != nil {
		return fmt.Errorf("create boxd app: %w", err)
	}

	if err := app.Run(ctx); err != nil {
		return fmt.Errorf("run boxd app: %w", err)
	}

	return nil
}
