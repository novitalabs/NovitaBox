package boxapi

import (
	"context"

	boxletclient "github.com/novitalabs/NovitaBox/internal/boxapi/boxlet"
	"github.com/novitalabs/NovitaBox/internal/boxapi/server"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"github.com/novitalabs/NovitaBox/internal/storage/store/sqlite"
)

type App struct {
	server *server.Server
	store  store.Store
	boxlet *boxletclient.Client
}

func New(cfg config.Config, logger *log.Logger) (*App, error) {
	dbPath := cfg.Storage.DBPath
	if dbPath == "" {
		dbPath = config.DefaultDBPath(cfg.RootDir)
	}
	logger.Info("opening sqlite database", "db_path", dbPath)

	st, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		return nil, err
	}

	boxlet, err := boxletclient.Dial(context.Background(), cfg.Boxlet.Addr)
	if err != nil {
		st.Close()
		return nil, err
	}

	return &App{
		server: server.New(cfg, logger.Component("boxapi"), st, boxlet.Sandbox, boxlet.Artifact),
		store:  st,
		boxlet: boxlet,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	defer a.store.Close()
	defer a.boxlet.Close()

	return a.server.Run(ctx)
}
