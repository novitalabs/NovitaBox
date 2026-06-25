package server

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/sandbox"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"github.com/novitalabs/NovitaBox/internal/storage/store/sqlite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func TestNodeService(t *testing.T) {
	listener := bufconn.Listen(bufSize)
	cfg := config.Default()
	cfg.RootDir = t.TempDir()
	st, err := sqlite.Open(context.Background(), filepath.Join(cfg.RootDir, "db", "novitabox.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer st.Close()

	s := New(cfg, log.New(testWriter{t: t}), st)

	go func() {
		if err := s.grpcServer.Serve(listener); err != nil {
			t.Errorf("serve boxlet grpc: %v", err)
		}
	}()
	defer s.grpcServer.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return listener.DialContext(ctx)
	}), grpc.WithInsecure())
	if err != nil {
		t.Fatalf("create grpc client: %v", err)
	}
	defer conn.Close()

	client := novitaboxv1.NewBoxletNodeServiceClient(conn)

	status, err := client.NodeStatus(context.Background(), &novitaboxv1.NodeStatusRequest{})
	if err != nil {
		t.Fatalf("node status: %v", err)
	}
	if !status.GetReady() {
		t.Fatal("expected node to be ready")
	}
	if status.GetRootDir() != cfg.RootDir {
		t.Fatalf("expected root dir %q, got %q", cfg.RootDir, status.GetRootDir())
	}

	runtimes, err := client.ListRuntimes(context.Background(), &novitaboxv1.ListRuntimesRequest{})
	if err != nil {
		t.Fatalf("list runtimes: %v", err)
	}
	if len(runtimes.GetRuntimes()) != 2 {
		t.Fatalf("expected 2 runtimes, got %d", len(runtimes.GetRuntimes()))
	}
}

func TestSandboxRuntimeSpecUsesBoxdInit(t *testing.T) {
	cfg := config.Default()
	cfg.RootDir = t.TempDir()
	svc := newSandboxService(cfg, log.NewNop(), nil)

	spec := svc.runtimeSpecForSandbox(store.SandboxRecord{
		ID:          "sbx-test",
		State:       sandbox.StateRunning,
		RuntimeType: "firecracker",
		TemplateID:  "tpl-test",
	})

	args := strings.Join(spec.GetKernel().GetKernelArgs(), " ")
	if !strings.Contains(args, "init=/novitabox/init") {
		t.Fatalf("kernel args = %q, want init=/novitabox/init", args)
	}
}

func TestCompleteRuntimeSpecFillsBoxdInit(t *testing.T) {
	cfg := config.Default()
	cfg.RootDir = t.TempDir()
	svc := newSandboxService(cfg, log.NewNop(), nil)

	spec := svc.completeRuntimeSpec(store.SandboxRecord{
		ID:          "sbx-test",
		State:       sandbox.StateRunning,
		RuntimeType: "firecracker",
		TemplateID:  "tpl-test",
	}, &novitaboxv1.RuntimeSpec{
		SandboxId:   "sbx-test",
		RuntimeType: novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER,
	})

	args := strings.Join(spec.GetKernel().GetKernelArgs(), " ")
	if !strings.Contains(args, "init=/novitabox/init") {
		t.Fatalf("kernel args = %q, want init=/novitabox/init", args)
	}
}

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
