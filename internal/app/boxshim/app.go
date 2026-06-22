package boxshim

import (
	"context"

	"github.com/novitalabs/NovitaBox/internal/boxshim/server"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

type App struct {
	server *server.Server
}

func New(cfg config.Config, logger *log.Logger) (*App, error) {
	return &App{server: server.New(cfg, logger.Component("boxshim"))}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.server.Run(ctx)
}
