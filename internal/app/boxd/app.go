package boxd

import (
	"context"

	"github.com/novitalabs/NovitaBox/internal/boxd/server"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

type App struct {
	server *server.Server
}

func New(cfg config.Config, logger *log.Logger) (*App, error) {
	return &App{server: server.New(cfg, logger.Component("boxd"))}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.server.Start(ctx)
}
