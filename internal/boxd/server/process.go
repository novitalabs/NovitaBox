package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/novitalabs/NovitaBox/internal/wsutil"
	"golang.org/x/net/websocket"
)

const processOutputBuffer = 64

type processManager struct {
	mu        sync.RWMutex
	processes map[string]*managedProcess
}

func newProcessManager() *processManager {
	return &processManager{processes: make(map[string]*managedProcess)}
}

func (m *processManager) add(proc *managedProcess) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processes[proc.ID] = proc
}

func (m *processManager) get(id string) (*managedProcess, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	proc, ok := m.processes[id]
	return proc, ok
}

func (m *processManager) list() []processInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]processInfo, 0, len(m.processes))
	for _, proc := range m.processes {
		out = append(out, proc.info())
	}
	return out
}

func (m *processManager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.processes, id)
}

type managedProcess struct {
	ID       string
	Tag      string
	Cmd      []string
	Cwd      string
	TTY      bool
	Started  time.Time
	cmd      *exec.Cmd
	terminal processTerminal
	manager  *processManager

	mu       sync.RWMutex
	exit     *processExit
	done     chan struct{}
	output   *outputHub
	waitOnce sync.Once
}

type processTerminal interface {
	io.Reader
	io.Writer
	io.Closer
}

type processExit struct {
	ExitCode int    `json:"exitCode"`
	Exited   bool   `json:"exited"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type processInfo struct {
	ID        string       `json:"id"`
	Tag       string       `json:"tag,omitempty"`
	PID       int          `json:"pid,omitempty"`
	Cmd       []string     `json:"cmd"`
	Cwd       string       `json:"cwd,omitempty"`
	TTY       bool         `json:"tty"`
	State     string       `json:"state"`
	StartedAt string       `json:"startedAt"`
	Exit      *processExit `json:"exit,omitempty"`
}

type outputHub struct {
	mu     sync.RWMutex
	closed bool
	subs   map[chan []byte]struct{}
}

func newOutputHub() *outputHub {
	return &outputHub{subs: make(map[chan []byte]struct{})}
}

func (h *outputHub) publish(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return
	}
	for sub := range h.subs {
		payload := append([]byte(nil), data...)
		select {
		case sub <- payload:
		default:
		}
	}
}

func (h *outputHub) subscribe() (chan []byte, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, processOutputBuffer)
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	h.subs[ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
	}
}

func (h *outputHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for sub := range h.subs {
		close(sub)
		delete(h.subs, sub)
	}
}

type startProcessRequest struct {
	Cmd  []string          `json:"cmd"`
	Cwd  string            `json:"cwd,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	TTY  bool              `json:"tty,omitempty"`
	Rows uint16            `json:"rows,omitempty"`
	Cols uint16            `json:"cols,omitempty"`
	Tag  string            `json:"tag,omitempty"`
}

type startProcessResponse struct {
	Process processInfo `json:"process"`
}

type listProcessesResponse struct {
	Processes []processInfo `json:"processes"`
}

type resizeProcessRequest struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

type signalProcessRequest struct {
	Signal string `json:"signal"`
}

type waitProcessResponse struct {
	Process processInfo `json:"process"`
}

type processEvent struct {
	Type    string       `json:"type"`
	Process processInfo  `json:"process,omitempty"`
	Exit    *processExit `json:"exit,omitempty"`
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, listProcessesResponse{Processes: s.processes.list()})
	case http.MethodPost:
		s.handleStartProcess(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleStartProcess(w http.ResponseWriter, r *http.Request) {
	var req startProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid process request body", http.StatusBadRequest)
		return
	}
	if len(req.Cmd) == 0 || req.Cmd[0] == "" {
		http.Error(w, "cmd is required", http.StatusBadRequest)
		return
	}

	env := make([]string, 0, len(req.Env))
	for key, value := range req.Env {
		env = append(env, key+"="+value)
	}

	id, err := newProcessID()
	if err != nil {
		http.Error(w, "generate process id failed", http.StatusInternalServerError)
		return
	}

	cmd, terminal, err := startPTYProcess(req.Cmd[0], req.Cmd[1:], req.Cwd, env, req.Cols, req.Rows)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	proc := &managedProcess{
		ID:       id,
		Tag:      req.Tag,
		Cmd:      append([]string(nil), req.Cmd...),
		Cwd:      req.Cwd,
		TTY:      true,
		Started:  time.Now().UTC(),
		cmd:      cmd,
		terminal: terminal,
		manager:  s.processes,
		done:     make(chan struct{}),
		output:   newOutputHub(),
	}
	s.processes.add(proc)
	proc.start()

	writeJSON(w, http.StatusCreated, startProcessResponse{Process: proc.info()})
}

func (s *Server) handleProcess(w http.ResponseWriter, r *http.Request) {
	id, action, ok := parseProcessPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if action == "connect" {
		websocket.Server{
			Handler:   websocket.Handler(s.handleProcessConnect),
			Handshake: acceptWebSocket,
		}.ServeHTTP(w, r)
		return
	}
	proc, exists := s.processes.get(id)
	if !exists {
		http.Error(w, "process not found", http.StatusNotFound)
		return
	}

	switch action {
	case "resize":
		s.handleResizeProcess(w, r, proc)
	case "signal":
		s.handleSignalProcess(w, r, proc)
	case "wait":
		s.handleWaitProcess(w, r, proc)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleProcessConnect(ws *websocket.Conn) {
	id, action, ok := parseProcessPath(ws.Request().URL.Path)
	if !ok || action != "connect" {
		_ = websocket.Message.Send(ws, []byte("unsupported process websocket route"))
		wsutil.CloseWebSocket(ws)
		return
	}
	proc, exists := s.processes.get(id)
	if !exists {
		_ = websocket.Message.Send(ws, []byte("process not found"))
		wsutil.CloseWebSocket(ws)
		return
	}
	defer wsutil.CloseWebSocket(ws)

	if err := websocket.JSON.Send(ws, processEvent{Type: "start", Process: proc.info()}); err != nil {
		return
	}

	output, cancel := proc.output.subscribe()
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		errCh <- proc.copyWebSocketInput(ws)
	}()
	go func() {
		errCh <- copyProcessOutput(ws, output, proc.done, proc)
	}()

	if err := <-errCh; err != nil && !errors.Is(err, io.EOF) {
		s.logger.Warn("process websocket closed with error", "process_id", id, "error", err)
	}
}

func (s *Server) handleResizeProcess(w http.ResponseWriter, r *http.Request, proc *managedProcess) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req resizeProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid resize request body", http.StatusBadRequest)
		return
	}
	if err := resizePTY(proc.terminal, req.Cols, req.Rows); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSignalProcess(w http.ResponseWriter, r *http.Request, proc *managedProcess) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req signalProcessRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	sig := syscall.SIGTERM
	switch req.Signal {
	case "SIGKILL", "KILL", "9":
		sig = syscall.SIGKILL
	case "SIGINT", "INT", "2":
		sig = syscall.SIGINT
	case "SIGHUP", "HUP", "1":
		sig = syscall.SIGHUP
	}
	if proc.cmd.Process == nil {
		http.Error(w, "process is not running", http.StatusConflict)
		return
	}
	if err := proc.cmd.Process.Signal(sig); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWaitProcess(w http.ResponseWriter, r *http.Request, proc *managedProcess) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	<-proc.done
	writeJSON(w, http.StatusOK, waitProcessResponse{Process: proc.info()})
}

func (p *managedProcess) start() {
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := p.terminal.Read(buf)
			if n > 0 {
				p.output.publish(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		err := p.cmd.Wait()
		exit := processExit{ExitCode: 0, Exited: true}
		if p.cmd.ProcessState != nil {
			exit.ExitCode = p.cmd.ProcessState.ExitCode()
			exit.Exited = p.cmd.ProcessState.Exited()
			exit.Status = p.cmd.ProcessState.String()
		}
		if err != nil {
			exit.Error = err.Error()
		}
		p.mu.Lock()
		p.exit = &exit
		p.mu.Unlock()
		_ = p.terminal.Close()
		p.output.close()
		close(p.done)
		time.AfterFunc(5*time.Minute, func() {
			p.manager.remove(p.ID)
		})
	}()
}

func (p *managedProcess) copyWebSocketInput(ws *websocket.Conn) error {
	var payload []byte
	for {
		if err := websocket.Message.Receive(ws, &payload); err != nil {
			if wsutil.IsCloseErr(err) {
				return nil
			}
			return err
		}
		if len(payload) == 0 {
			continue
		}
		if _, err := p.terminal.Write(payload); err != nil {
			if wsutil.IsCloseErr(err) {
				return nil
			}
			return err
		}
	}
}

func copyProcessOutput(ws *websocket.Conn, output <-chan []byte, done <-chan struct{}, proc *managedProcess) error {
	for {
		select {
		case payload, ok := <-output:
			if !ok {
				return websocket.JSON.Send(ws, processEvent{Type: "end", Exit: proc.exitInfo()})
			}
			if err := websocket.Message.Send(ws, payload); err != nil {
				if wsutil.IsCloseErr(err) {
					return nil
				}
				return err
			}
		case <-done:
			return websocket.JSON.Send(ws, processEvent{Type: "end", Exit: proc.exitInfo()})
		}
	}
}

func (p *managedProcess) info() processInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state := "running"
	var exit *processExit
	if p.exit != nil {
		state = "exited"
		copyExit := *p.exit
		exit = &copyExit
	}
	pid := 0
	if p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	return processInfo{
		ID:        p.ID,
		Tag:       p.Tag,
		PID:       pid,
		Cmd:       append([]string(nil), p.Cmd...),
		Cwd:       p.Cwd,
		TTY:       p.TTY,
		State:     state,
		StartedAt: p.Started.Format(time.RFC3339Nano),
		Exit:      exit,
	}
}

func (p *managedProcess) exitInfo() *processExit {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.exit == nil {
		return nil
	}
	out := *p.exit
	return &out
}

func parseProcessPath(path string) (string, string, bool) {
	const prefix = "/processes/"
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		return "", "", false
	}
	rest := path[len(prefix):]
	slash := -1
	for i := range rest {
		if rest[i] == '/' {
			slash = i
			break
		}
	}
	if slash <= 0 || slash == len(rest)-1 {
		return "", "", false
	}
	return rest[:slash], rest[slash+1:], true
}

func newProcessID() (string, error) {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "prc-" + hex.EncodeToString(raw[:]), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
