package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	shimruntime "github.com/novitalabs/NovitaBox/internal/boxshim/runtime"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Server struct {
	cfg        config.Config
	logger     *log.Logger
	grpcServer *grpc.Server
	runtime    *runtimeService
	stopOnce   chan struct{}
}

func New(cfg config.Config, logger *log.Logger) *Server {
	driver := newRuntimeDriver(cfg, logger.Component("runtime"))
	s := &Server{
		cfg:        cfg,
		logger:     logger,
		grpcServer: grpc.NewServer(),
		stopOnce:   make(chan struct{}),
	}
	s.runtime = newRuntimeService(driver, s.requestStop)
	novitaboxv1.RegisterBoxShimServer(s.grpcServer, s.runtime)
	reflection.Register(s.grpcServer)

	return s
}

func newRuntimeDriver(cfg config.Config, logger *log.Logger) shimruntime.Driver {
	switch strings.ToLower(cfg.Boxshim.RuntimeDriver) {
	case "", "stub":
		return shimruntime.NewStubDriver(cfg, logger.Component("stub"))
	case "firecracker":
		return shimruntime.NewFirecrackerDriver(cfg, logger.Component("firecracker"))
	case "gvisor", "container":
		return shimruntime.NewGVisorDriver(cfg, logger.Component("gvisor"))
	default:
		return shimruntime.NewUnsupportedDriver(fmt.Sprintf("unknown runtime driver %q", cfg.Boxshim.RuntimeDriver))
	}
}

func (s *Server) Run(ctx context.Context) error {
	socketPath := s.cfg.Boxshim.SocketPath
	if socketPath == "" {
		return errors.New("boxshim socket path is required")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if err := os.RemoveAll(socketPath); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}

	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen boxshim socket: %w", err)
	}
	defer os.Remove(socketPath)

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("starting boxshim", "socket", socketPath)
		errCh <- s.grpcServer.Serve(lis)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	case <-s.stopOnce:
		s.stopGRPCServer()
		s.logger.Info("stopped boxshim")
		return nil
	case <-ctx.Done():
		s.stopGRPCServer()
		s.logger.Info("stopped boxshim")
		return nil
	}
}

func (s *Server) requestStop() {
	select {
	case <-s.stopOnce:
	default:
		close(s.stopOnce)
	}
}

func (s *Server) stopGRPCServer() {
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
}

type runtimeService struct {
	novitaboxv1.UnimplementedBoxShimServer
	driver shimruntime.Driver
	onKill func()
}

func newRuntimeService(driver shimruntime.Driver, onKill func()) *runtimeService {
	return &runtimeService{driver: driver, onKill: onKill}
}

func (s *runtimeService) CreateRuntime(ctx context.Context, req *novitaboxv1.CreateRuntimeRequest) (*novitaboxv1.RuntimeInfo, error) {
	return s.driver.Create(ctx, req.GetRuntimeSpec())
}

func (s *runtimeService) PauseRuntime(ctx context.Context, req *novitaboxv1.PauseRuntimeRequest) (*novitaboxv1.RuntimeInfo, error) {
	return s.driver.Pause(ctx, req.GetSandboxId())
}

func (s *runtimeService) ResumeRuntime(ctx context.Context, req *novitaboxv1.ResumeRuntimeRequest) (*novitaboxv1.RuntimeInfo, error) {
	return s.driver.Resume(ctx, req.GetRuntimeSpec())
}

func (s *runtimeService) KillRuntime(ctx context.Context, req *novitaboxv1.KillRuntimeRequest) (*emptypb.Empty, error) {
	if err := s.driver.Kill(ctx, req.GetSandboxId()); err != nil {
		return nil, err
	}
	if s.onKill != nil {
		go s.onKill()
	}

	return &emptypb.Empty{}, nil
}

func (s *runtimeService) StartRuntime(ctx context.Context, req *novitaboxv1.StartRuntimeRequest) (*novitaboxv1.RuntimeInfo, error) {
	return s.driver.Start(ctx, req.GetRuntimeSpec())
}

func (s *runtimeService) StopRuntime(ctx context.Context, req *novitaboxv1.StopRuntimeRequest) (*novitaboxv1.RuntimeInfo, error) {
	return s.driver.Stop(ctx, req.GetSandboxId(), time.Duration(req.GetTimeoutSeconds())*time.Second)
}

func (s *runtimeService) RebootRuntime(ctx context.Context, req *novitaboxv1.RebootRuntimeRequest) (*novitaboxv1.RuntimeInfo, error) {
	return s.driver.Reboot(ctx, req.GetSandboxId(), time.Duration(req.GetTimeoutSeconds())*time.Second)
}

func (s *runtimeService) Status(ctx context.Context, req *novitaboxv1.StatusRequest) (*novitaboxv1.RuntimeInfo, error) {
	return s.driver.Status(ctx, req.GetSandboxId())
}

func (s *runtimeService) Capabilities(ctx context.Context, req *novitaboxv1.CapabilitiesRequest) (*novitaboxv1.RuntimeCapabilities, error) {
	return s.driver.Capabilities(ctx, req.GetRuntimeType())
}
