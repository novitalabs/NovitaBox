package boxproxy

import (
	"context"
	"fmt"

	"github.com/novitalabs/NovitaBox/internal/boxproxy/server"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	"github.com/novitalabs/NovitaBox/internal/storage/store/sqlite"
)

type App struct {
	server *server.Server
	store  *sqlite.Store
}

func New(cfg config.Config, logger *log.Logger) (*App, error) {
	dbPath := cfg.Storage.DBPath
	if dbPath == "" {
		dbPath = config.DefaultDBPath(cfg.RootDir)
	}
	logger.Info("opening sqlite database", "db_path", dbPath)
	st, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	return &App{
		server: server.New(cfg, logger.Component("boxproxy"), st),
		store:  st,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	defer a.store.Close()
	return a.server.Start(ctx)
}
