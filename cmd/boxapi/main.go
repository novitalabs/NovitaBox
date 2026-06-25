package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/novitalabs/NovitaBox/internal/app/boxapi"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

func main() {
	defaults := config.Default()
	opts := config.ServiceOptions{
		RootDir:    defaults.RootDir,
		Addr:       defaults.BoxAPI.Addr,
		BoxletAddr: defaults.Boxlet.Addr,
	}

	cmd := &cobra.Command{
		Use:   "boxapi",
		Short: "Run the NovitaBox API service",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.ApplyServiceOptions(defaults, "boxapi", opts)
			if err != nil {
				return err
			}

			return run(cfg)
		},
	}
	cmd.Flags().StringVar(&opts.RootDir, "root", opts.RootDir, "NovitaBox root directory")
	cmd.Flags().StringVar(&opts.Addr, "addr", opts.Addr, "boxapi listen address")
	cmd.Flags().IntVar(&opts.Port, "port", 0, "boxapi listen port, overrides the port in --addr")
	cmd.Flags().StringVar(&opts.DBPath, "db-path", opts.DBPath, "SQLite database path, defaults to $root/db/novitabox.db")
	cmd.Flags().StringVar(&opts.BoxletAddr, "boxlet-addr", opts.BoxletAddr, "boxlet gRPC address")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "boxapi: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := boxapi.New(cfg, log.New(os.Stdout))
	if err != nil {
		return fmt.Errorf("create boxapi app: %w", err)
	}

	if err := app.Run(ctx); err != nil {
		return fmt.Errorf("run boxapi app: %w", err)
	}

	return nil
}
