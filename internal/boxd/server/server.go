package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	"golang.org/x/net/websocket"
)

type Server struct {
	cfg        config.Config
	logger     *log.Logger
	httpServer *http.Server
	processes  *processManager
	startedAt  time.Time
}

func New(cfg config.Config, logger *log.Logger) *Server {
	s := &Server{cfg: cfg, logger: logger, processes: newProcessManager(), startedAt: time.Now()}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/admin/reexec", s.handleReexec)
	mux.HandleFunc("/exec", s.handleExec)
	mux.HandleFunc("/processes", s.handleProcesses)
	mux.HandleFunc("/processes/", s.handleProcess)
	mux.HandleFunc("/process.Process/List", s.handleConnectList)
	mux.HandleFunc("/process.Process/Start", s.handleConnectStart)
	mux.HandleFunc("/process.Process/Connect", s.handleConnectAttach)
	mux.HandleFunc("/process.Process/Update", s.handleConnectUpdate)
	mux.HandleFunc("/process.Process/SendInput", s.handleConnectSendInput)
	mux.HandleFunc("/process.Process/SendSignal", s.handleConnectSendSignal)
	s.httpServer = &http.Server{
		Addr:              cfg.Boxd.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

func acceptWebSocket(config *websocket.Config, req *http.Request) error {
	origin, err := websocket.Origin(config, req)
	if err != nil {
		return err
	}
	config.Origin = origin
	return nil
}

func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("starting boxd", "addr", s.cfg.Boxd.Addr)
	errCh := make(chan error, 1)
	go func() {
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
		return s.Stop(context.Background())
	}
}

func (s *Server) Stop(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}

	s.logger.Info("stopped boxd")
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":            "ok",
		"service":           "boxd",
		"startedAtUnixNano": s.startedAt.UnixNano(),
	})
}

type reexecRequest struct {
	Path        string   `json:"path"`
	Args        []string `json:"args,omitempty"`
	MountDevice string   `json:"mountDevice,omitempty"`
	MountPath   string   `json:"mountPath,omitempty"`
}

func (s *Server) handleReexec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	req := reexecRequest{}
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid reexec request body", http.StatusBadRequest)
			return
		}
	}

	targetPath := req.Path
	if targetPath == "" {
		targetPath = s.cfg.Template.BoxdGuestPath
	}
	if targetPath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	if req.MountDevice != "" && req.MountPath != "" {
		if err := ensureReadonlyMount(req.MountDevice, req.MountPath, targetPath); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if info, err := os.Stat(targetPath); err != nil {
		http.Error(w, fmt.Sprintf("stat reexec target: %v", err), http.StatusInternalServerError)
		return
	} else if info.IsDir() {
		http.Error(w, "reexec target is a directory", http.StatusBadRequest)
		return
	}

	args := append([]string{targetPath}, req.Args...)
	if len(req.Args) == 0 {
		args = append([]string{targetPath}, os.Args[1:]...)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":            "reexecing",
		"startedAtUnixNano": s.startedAt.UnixNano(),
	})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := syscall.Exec(targetPath, args, os.Environ()); err != nil {
			s.logger.Error("reexec boxd failed", "path", targetPath, "error", err)
		}
	}()
}

type execRequest struct {
	Cmd     []string          `json:"cmd"`
	EnvVars map[string]string `json:"envVars,omitempty"`
}

type execResponse struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid exec request body", http.StatusBadRequest)
		return
	}
	if len(req.Cmd) == 0 || req.Cmd[0] == "" {
		http.Error(w, "cmd is required", http.StatusBadRequest)
		return
	}

	cmd := exec.CommandContext(r.Context(), req.Cmd[0], req.Cmd[1:]...)
	cmd.Env = os.Environ()
	for key, value := range req.EnvVars {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			stderr.WriteString(err.Error())
		}
	}

	status := http.StatusOK
	if exitCode != 0 {
		status = http.StatusUnprocessableEntity
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(execResponse{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	})
}
