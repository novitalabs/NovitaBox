package server

import (
	"context"
	"net"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type Server struct {
	cfg        config.Config
	logger     *log.Logger
	store      store.Store
	grpcServer *grpc.Server
}

func New(cfg config.Config, logger *log.Logger, store store.Store) *Server {
	s := &Server{
		cfg:        cfg,
		logger:     logger,
		store:      store,
		grpcServer: grpc.NewServer(),
	}
	s.registerServices()

	return s
}

func (s *Server) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.cfg.Boxlet.Addr)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("starting boxlet", "addr", s.cfg.Boxlet.Addr)
		errCh <- s.grpcServer.Serve(lis)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		stopped := make(chan struct{})
		go func() {
			s.grpcServer.GracefulStop()
			close(stopped)
		}()

		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			s.grpcServer.Stop()
		}

		s.logger.Info("stopped boxlet")
		return nil
	}
}

func (s *Server) registerServices() {
	novitaboxv1.RegisterBoxletSandboxServiceServer(s.grpcServer, newSandboxService(s.cfg, s.logger.Component("sandbox"), s.store))
	novitaboxv1.RegisterBoxletArtifactServiceServer(s.grpcServer, newArtifactService(s.cfg, s.logger.Component("artifact"), s.store))
	novitaboxv1.RegisterBoxletNodeServiceServer(s.grpcServer, newNodeService(s.cfg))
	reflection.Register(s.grpcServer)
}
