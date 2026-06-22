package server

import (
	"context"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

type Server struct {
	cfg    config.Config
	logger *log.Logger
}

func New(cfg config.Config, logger *log.Logger) *Server {
	return &Server{cfg: cfg, logger: logger}
}

func (s *Server) Start(ctx context.Context) {
	s.logger.Info("starting boxproxy", "addr", s.cfg.BoxProxy.Addr)
}

func (s *Server) Stop(ctx context.Context) {
	s.logger.Info("stopped boxproxy")
}
