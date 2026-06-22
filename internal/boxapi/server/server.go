package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
)

type Server struct {
	cfg        config.Config
	logger     *log.Logger
	store      store.Store
	sandbox    novitaboxv1.BoxletSandboxServiceClient
	artifact   novitaboxv1.BoxletArtifactServiceClient
	httpServer *http.Server
}

func New(cfg config.Config, logger *log.Logger, store store.Store, sandbox novitaboxv1.BoxletSandboxServiceClient, artifact novitaboxv1.BoxletArtifactServiceClient) *Server {
	s := &Server{cfg: cfg, logger: logger, store: store, sandbox: sandbox, artifact: artifact}
	s.httpServer = &http.Server{
		Addr:              cfg.BoxAPI.Addr,
		Handler:           s.router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	return s
}

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("starting boxapi", "addr", s.cfg.BoxAPI.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}

		s.logger.Info("stopped boxapi")
		return nil
	}
}
