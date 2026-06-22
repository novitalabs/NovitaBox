package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
)

type Server struct {
	cfg        config.Config
	logger     *log.Logger
	httpServer *http.Server
}

func New(cfg config.Config, logger *log.Logger) *Server {
	s := &Server{cfg: cfg, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/exec", s.handleExec)
	s.httpServer = &http.Server{
		Addr:              cfg.Boxd.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
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
		"status":  "ok",
		"service": "boxd",
	})
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
