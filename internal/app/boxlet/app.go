package boxlet

import (
	"context"

	"github.com/novitalabs/NovitaBox/internal/boxlet/server"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
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
		dbPath = layout.New(cfg.RootDir).DBPath()
	}

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
