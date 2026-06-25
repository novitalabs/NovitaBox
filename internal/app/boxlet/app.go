package boxlet

import (
	"context"

	"github.com/novitalabs/NovitaBox/internal/boxlet/server"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"github.com/novitalabs/NovitaBox/internal/storage/store/sqlite"
)

type App struct {
	server *server.Server
	store  store.Store
}

func New(cfg config.Config, logger *log.Logger) (*App, error) {
	dbPath := cfg.Storage.DBPath
	if dbPath == "" {
		dbPath = config.DefaultDBPath(cfg.RootDir)
	}
	logger.Info("opening sqlite database", "db_path", dbPath)
	logger.Info("using template defaults",
		"template_kernel", cfg.Template.KernelPath,
		"template_boxd_bin", cfg.Template.BoxdBinaryPath,
		"template_boxd_guest_path", cfg.Template.BoxdGuestPath,
		"template_boxd_guest_addr", cfg.Template.BoxdGuestAddr,
	)

	st, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		return nil, err
	}

	return &App{
		server: server.New(cfg, logger.Component("boxlet"), st),
		store:  st,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	defer a.store.Close()

	return a.server.Run(ctx)
}
