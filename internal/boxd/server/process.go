package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
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

func (m *processManager) getByPID(pid uint32) (*managedProcess, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, proc := range m.processes {
		if proc.pid() == pid {
			return proc, true
		}
	}
	return nil, false
}

func (m *processManager) getByTag(tag string) (*managedProcess, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, proc := range m.processes {
		if proc.Tag == tag {
			return proc, true
		}
	}
	return nil, false
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

type connectProcessConfig struct {
	Cmd  string            `json:"cmd"`
	Args []string          `json:"args,omitempty"`
	Envs map[string]string `json:"envs,omitempty"`
	Cwd  *string           `json:"cwd,omitempty"`
}

type connectPTY struct {
	Size *connectPTYSize `json:"size,omitempty"`
}

type connectPTYSize struct {
	Cols uint32 `json:"cols,omitempty"`
	Rows uint32 `json:"rows,omitempty"`
}

type connectProcessSelector struct {
	PID uint32 `json:"pid,omitempty"`
	Tag string `json:"tag,omitempty"`
}

type connectStartRequest struct {
	Process *connectProcessConfig `json:"process"`
	PTY     *connectPTY           `json:"pty,omitempty"`
	Tag     *string               `json:"tag,omitempty"`
	Stdin   *bool                 `json:"stdin,omitempty"`
}

type connectConnectRequest struct {
	Process *connectProcessSelector `json:"process"`
}

type connectUpdateRequest struct {
	Process *connectProcessSelector `json:"process"`
	PTY     *connectPTY             `json:"pty,omitempty"`
}

type connectSendInputRequest struct {
	Process *connectProcessSelector `json:"process"`
	Input   *connectProcessInput    `json:"input"`
}

type connectSendSignalRequest struct {
	Process *connectProcessSelector `json:"process"`
	Signal  uint32                  `json:"signal"`
}

type connectProcessInput struct {
	Stdin string `json:"stdin,omitempty"`
	PTY   string `json:"pty,omitempty"`
}

type connectListResponse struct {
	Processes []connectProcessInfo `json:"processes"`
}

type connectProcessInfo struct {
	Config connectProcessConfig `json:"config"`
	PID    uint32               `json:"pid"`
	Tag    string               `json:"tag,omitempty"`
}

type connectStreamResponse struct {
	Event connectProcessEvent `json:"event"`
}

type connectProcessEvent struct {
	Start     *connectStartEvent     `json:"start,omitempty"`
	Data      *connectDataEvent      `json:"data,omitempty"`
	End       *connectEndEvent       `json:"end,omitempty"`
	Keepalive *connectKeepaliveEvent `json:"keepalive,omitempty"`
}

type connectStartEvent struct {
	PID uint32 `json:"pid"`
}

type connectDataEvent struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	PTY    string `json:"pty,omitempty"`
}

type connectEndEvent struct {
	ExitCode int32   `json:"exitCode"`
	Exited   bool    `json:"exited"`
	Status   string  `json:"status"`
	Error    *string `json:"error,omitempty"`
}

type connectKeepaliveEvent struct{}

type connectCodec int

const (
	connectCodecJSON connectCodec = iota
	connectCodecProto
)

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

func (s *Server) handleConnectList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	processes := s.processes.list()
	out := make([]connectProcessInfo, 0, len(processes))
	for _, proc := range processes {
		out = append(out, connectProcessInfoFromProcessInfo(proc))
	}
	writeConnectUnary(w, connectCodecFromRequest(r), connectListResponse{Processes: out})
}

func (s *Server) handleConnectStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	codec := connectCodecFromRequest(r)
	var req connectStartRequest
	if err := readConnectRequest(r, codec, &req); err != nil {
		http.Error(w, "invalid connect start request body", http.StatusBadRequest)
		return
	}
	if req.Process == nil || req.Process.Cmd == "" {
		http.Error(w, "process.cmd is required", http.StatusBadRequest)
		return
	}

	rows, cols := connectPTYSizeValue(req.PTY, 24, 80)
	cmd, terminal, err := startPTYProcess(req.Process.Cmd, req.Process.Args, connectString(req.Process.Cwd), connectEnv(req.Process.Envs), cols, rows)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id, err := newProcessID()
	if err != nil {
		_ = terminal.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		http.Error(w, "generate process id failed", http.StatusInternalServerError)
		return
	}
	proc := &managedProcess{
		ID:       id,
		Tag:      connectString(req.Tag),
		Cmd:      append([]string{req.Process.Cmd}, req.Process.Args...),
		Cwd:      connectString(req.Process.Cwd),
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
	s.streamConnectProcess(w, r, codec, proc)
}

func (s *Server) handleConnectAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	codec := connectCodecFromRequest(r)
	var req connectConnectRequest
	if err := readConnectRequest(r, codec, &req); err != nil {
		http.Error(w, "invalid connect request body", http.StatusBadRequest)
		return
	}
	proc, ok := s.processFromSelector(req.Process)
	if !ok {
		http.Error(w, "process not found", http.StatusNotFound)
		return
	}
	s.streamConnectProcess(w, r, codec, proc)
}

func (s *Server) handleConnectUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	codec := connectCodecFromRequest(r)
	var req connectUpdateRequest
	if err := readConnectRequest(r, codec, &req); err != nil {
		http.Error(w, "invalid update request body", http.StatusBadRequest)
		return
	}
	proc, ok := s.processFromSelector(req.Process)
	if !ok {
		http.Error(w, "process not found", http.StatusNotFound)
		return
	}
	if req.PTY != nil {
		rows, cols := connectPTYSizeValue(req.PTY, 24, 80)
		if err := resizePTY(proc.terminal, cols, rows); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	writeConnectUnary(w, codec, map[string]any{})
}

func (s *Server) handleConnectSendInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	codec := connectCodecFromRequest(r)
	var req connectSendInputRequest
	if err := readConnectRequest(r, codec, &req); err != nil {
		http.Error(w, "invalid send input request body", http.StatusBadRequest)
		return
	}
	proc, ok := s.processFromSelector(req.Process)
	if !ok {
		http.Error(w, "process not found", http.StatusNotFound)
		return
	}
	if req.Input == nil {
		http.Error(w, "input is required", http.StatusBadRequest)
		return
	}
	payload, err := decodeConnectInput(req.Input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(payload) > 0 {
		if _, err := proc.terminal.Write(payload); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeConnectUnary(w, codec, map[string]any{})
}

func (s *Server) handleConnectSendSignal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	codec := connectCodecFromRequest(r)
	var req connectSendSignalRequest
	if err := readConnectRequest(r, codec, &req); err != nil {
		http.Error(w, "invalid send signal request body", http.StatusBadRequest)
		return
	}
	proc, ok := s.processFromSelector(req.Process)
	if !ok {
		http.Error(w, "process not found", http.StatusNotFound)
		return
	}
	if err := signalProcess(proc, signalFromNumber(req.Signal)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeConnectUnary(w, codec, map[string]any{})
}

func (s *Server) processFromSelector(selector *connectProcessSelector) (*managedProcess, bool) {
	if selector == nil {
		return nil, false
	}
	if selector.PID > 0 {
		return s.processes.getByPID(selector.PID)
	}
	if selector.Tag != "" {
		return s.processes.getByTag(selector.Tag)
	}
	return nil, false
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
	if err := signalProcess(proc, sig); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func signalFromNumber(signal uint32) syscall.Signal {
	switch signal {
	case 9:
		return syscall.SIGKILL
	case 2:
		return syscall.SIGINT
	case 1:
		return syscall.SIGHUP
	default:
		return syscall.SIGTERM
	}
}

func signalProcess(proc *managedProcess, sig syscall.Signal) error {
	if proc.cmd.Process == nil {
		return fmt.Errorf("process is not running")
	}
	return proc.cmd.Process.Signal(sig)
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

func (s *Server) streamConnectProcess(w http.ResponseWriter, r *http.Request, codec connectCodec, proc *managedProcess) {
	w.Header().Set("Content-Type", connectContentType(codec))
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	writer := newConnectStreamWriter(w, codec)
	if err := writer.write(connectStreamResponse{Event: connectProcessEvent{Start: &connectStartEvent{PID: proc.pid()}}}); err != nil {
		return
	}

	output, cancel := proc.output.subscribe()
	defer cancel()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case payload, ok := <-output:
			if !ok {
				_ = writer.write(connectStreamResponse{Event: connectProcessEvent{End: connectEndFromProcessExit(proc.exitInfo())}})
				_ = writer.end()
				return
			}
			if len(payload) == 0 {
				continue
			}
			err := writer.write(connectStreamResponse{Event: connectProcessEvent{
				Data: &connectDataEvent{PTY: base64.StdEncoding.EncodeToString(payload)},
			}})
			if err != nil {
				return
			}
		case <-proc.done:
			_ = writer.write(connectStreamResponse{Event: connectProcessEvent{End: connectEndFromProcessExit(proc.exitInfo())}})
			_ = writer.end()
			return
		case <-keepalive.C:
			if err := writer.write(connectStreamResponse{Event: connectProcessEvent{Keepalive: &connectKeepaliveEvent{}}}); err != nil {
				return
			}
		case <-r.Context().Done():
			return
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

func (p *managedProcess) pid() uint32 {
	if p == nil || p.cmd == nil || p.cmd.Process == nil || p.cmd.Process.Pid <= 0 {
		return 0
	}
	return uint32(p.cmd.Process.Pid)
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

func connectProcessInfoFromProcessInfo(proc processInfo) connectProcessInfo {
	cfg := connectProcessConfig{}
	if len(proc.Cmd) > 0 {
		cfg.Cmd = proc.Cmd[0]
		cfg.Args = append([]string(nil), proc.Cmd[1:]...)
	}
	if proc.Cwd != "" {
		cfg.Cwd = &proc.Cwd
	}
	return connectProcessInfo{
		Config: cfg,
		PID:    uint32(proc.PID),
		Tag:    proc.Tag,
	}
}

func connectEndFromProcessExit(exit *processExit) *connectEndEvent {
	if exit == nil {
		return &connectEndEvent{Exited: true}
	}
	out := &connectEndEvent{
		ExitCode: int32(exit.ExitCode),
		Exited:   exit.Exited,
		Status:   exit.Status,
	}
	if exit.Error != "" {
		out.Error = &exit.Error
	}
	return out
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

func connectPTYSizeValue(pty *connectPTY, fallbackRows uint16, fallbackCols uint16) (uint16, uint16) {
	if pty == nil || pty.Size == nil {
		return fallbackRows, fallbackCols
	}
	rows := uint16(pty.Size.Rows)
	cols := uint16(pty.Size.Cols)
	if rows == 0 {
		rows = fallbackRows
	}
	if cols == 0 {
		cols = fallbackCols
	}
	return rows, cols
}

func connectString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func connectEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func decodeConnectInput(input *connectProcessInput) ([]byte, error) {
	raw := input.PTY
	if raw == "" {
		raw = input.Stdin
	}
	if raw == "" {
		return nil, nil
	}
	payload, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode input bytes: %w", err)
	}
	return payload, nil
}

func connectCodecFromRequest(r *http.Request) connectCodec {
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "proto") {
		return connectCodecProto
	}
	return connectCodecJSON
}

func connectContentType(codec connectCodec) string {
	if codec == connectCodecProto {
		return "application/connect+proto"
	}
	return "application/connect+json"
}

func readConnectRequest(r *http.Request, codec connectCodec, out any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	body = bytes.TrimSpace(body)
	if len(body) >= 5 && (body[0] == 0 || body[0] == 1) {
		size := binary.BigEndian.Uint32(body[1:5])
		if int(size) <= len(body)-5 {
			body = body[5 : 5+size]
		}
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	if codec == connectCodecProto {
		return unmarshalConnectProtoRequest(body, out)
	}
	return json.Unmarshal(body, out)
}

type connectStreamWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	codec   connectCodec
}

func newConnectStreamWriter(w http.ResponseWriter, codec connectCodec) connectStreamWriter {
	flusher, _ := w.(http.Flusher)
	return connectStreamWriter{w: w, flusher: flusher, codec: codec}
}

func (w connectStreamWriter) write(value any) error {
	payload, err := marshalConnectResponse(w.codec, value)
	if err != nil {
		return err
	}
	if err := writeConnectEnvelope(w.w, 0, payload); err != nil {
		return err
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
	return nil
}

func (w connectStreamWriter) end() error {
	payload := []byte("{}")
	if w.codec == connectCodecProto {
		payload = nil
	}
	if err := writeConnectEnvelope(w.w, 2, payload); err != nil {
		return err
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
	return nil
}

func writeConnectEnvelope(w io.Writer, flags byte, payload []byte) error {
	var header [5]byte
	header[0] = flags
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func marshalConnectResponse(codec connectCodec, value any) ([]byte, error) {
	if codec == connectCodecProto {
		return marshalConnectProto(value)
	}
	return json.Marshal(value)
}

func unmarshalConnectProtoRequest(payload []byte, out any) error {
	switch req := out.(type) {
	case *connectStartRequest:
		return unmarshalProtoStartRequest(payload, req)
	case *connectConnectRequest:
		var selector connectProcessSelector
		if err := unmarshalProtoSelectorMessage(payload, 1, &selector); err != nil {
			return err
		}
		req.Process = &selector
		return nil
	case *connectUpdateRequest:
		return unmarshalProtoUpdateRequest(payload, req)
	case *connectSendInputRequest:
		return unmarshalProtoSendInputRequest(payload, req)
	case *connectSendSignalRequest:
		return unmarshalProtoSendSignalRequest(payload, req)
	default:
		if len(payload) == 0 {
			return nil
		}
		return fmt.Errorf("unsupported proto request %T", out)
	}
}

func marshalConnectProto(value any) ([]byte, error) {
	switch v := value.(type) {
	case connectStreamResponse:
		event := marshalProtoProcessEvent(v.Event)
		return protoBytesField(1, event), nil
	case connectListResponse:
		var out []byte
		for _, proc := range v.Processes {
			out = append(out, protoBytesField(1, marshalProtoProcessInfo(proc))...)
		}
		return out, nil
	case map[string]any:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported proto response %T", value)
	}
}

func marshalProtoProcessInfo(proc connectProcessInfo) []byte {
	var out []byte
	out = append(out, protoBytesField(1, marshalProtoProcessConfig(proc.Config))...)
	if proc.PID > 0 {
		out = append(out, protoVarintField(2, uint64(proc.PID))...)
	}
	if proc.Tag != "" {
		out = append(out, protoStringField(3, proc.Tag)...)
	}
	return out
}

func marshalProtoProcessConfig(cfg connectProcessConfig) []byte {
	var out []byte
	if cfg.Cmd != "" {
		out = append(out, protoStringField(1, cfg.Cmd)...)
	}
	for _, arg := range cfg.Args {
		out = append(out, protoStringField(2, arg)...)
	}
	for key, value := range cfg.Envs {
		var entry []byte
		entry = append(entry, protoStringField(1, key)...)
		entry = append(entry, protoStringField(2, value)...)
		out = append(out, protoBytesField(3, entry)...)
	}
	if cfg.Cwd != nil {
		out = append(out, protoStringField(4, *cfg.Cwd)...)
	}
	return out
}

func marshalProtoProcessEvent(event connectProcessEvent) []byte {
	switch {
	case event.Start != nil:
		return protoBytesField(1, protoVarintField(1, uint64(event.Start.PID)))
	case event.Data != nil:
		return protoBytesField(2, marshalProtoDataEvent(event.Data))
	case event.End != nil:
		return protoBytesField(3, marshalProtoEndEvent(event.End))
	case event.Keepalive != nil:
		return protoBytesField(4, nil)
	default:
		return nil
	}
}

func marshalProtoDataEvent(event *connectDataEvent) []byte {
	switch {
	case event.Stdout != "":
		payload, _ := base64.StdEncoding.DecodeString(event.Stdout)
		return protoBytesField(1, payload)
	case event.Stderr != "":
		payload, _ := base64.StdEncoding.DecodeString(event.Stderr)
		return protoBytesField(2, payload)
	case event.PTY != "":
		payload, _ := base64.StdEncoding.DecodeString(event.PTY)
		return protoBytesField(3, payload)
	default:
		return nil
	}
}

func marshalProtoEndEvent(event *connectEndEvent) []byte {
	var out []byte
	out = append(out, protoVarintField(1, uint64(encodeZigZag32(event.ExitCode)))...)
	out = append(out, protoVarintField(2, boolVarint(event.Exited))...)
	if event.Status != "" {
		out = append(out, protoStringField(3, event.Status)...)
	}
	if event.Error != nil {
		out = append(out, protoStringField(4, *event.Error)...)
	}
	return out
}

func unmarshalProtoStartRequest(payload []byte, req *connectStartRequest) error {
	return walkProtoFields(payload, func(field int, wire int, value []byte, u uint64) error {
		switch field {
		case 1:
			cfg, err := unmarshalProtoProcessConfig(value)
			if err != nil {
				return err
			}
			req.Process = cfg
		case 2:
			pty, err := unmarshalProtoPTY(value)
			if err != nil {
				return err
			}
			req.PTY = pty
		case 3:
			tag := string(value)
			req.Tag = &tag
		case 4:
			stdin := u != 0
			req.Stdin = &stdin
		}
		return nil
	})
}

func unmarshalProtoUpdateRequest(payload []byte, req *connectUpdateRequest) error {
	return walkProtoFields(payload, func(field int, wire int, value []byte, _ uint64) error {
		switch field {
		case 1:
			var selector connectProcessSelector
			if err := unmarshalProtoSelector(value, &selector); err != nil {
				return err
			}
			req.Process = &selector
		case 2:
			pty, err := unmarshalProtoPTY(value)
			if err != nil {
				return err
			}
			req.PTY = pty
		}
		return nil
	})
}

func unmarshalProtoSendInputRequest(payload []byte, req *connectSendInputRequest) error {
	return walkProtoFields(payload, func(field int, wire int, value []byte, _ uint64) error {
		switch field {
		case 1:
			var selector connectProcessSelector
			if err := unmarshalProtoSelector(value, &selector); err != nil {
				return err
			}
			req.Process = &selector
		case 2:
			input, err := unmarshalProtoProcessInput(value)
			if err != nil {
				return err
			}
			req.Input = input
		}
		return nil
	})
}

func unmarshalProtoSendSignalRequest(payload []byte, req *connectSendSignalRequest) error {
	return walkProtoFields(payload, func(field int, wire int, value []byte, u uint64) error {
		switch field {
		case 1:
			var selector connectProcessSelector
			if err := unmarshalProtoSelector(value, &selector); err != nil {
				return err
			}
			req.Process = &selector
		case 2:
			req.Signal = uint32(u)
		}
		return nil
	})
}

func unmarshalProtoSelectorMessage(payload []byte, fieldNumber int, out *connectProcessSelector) error {
	return walkProtoFields(payload, func(field int, wire int, value []byte, _ uint64) error {
		if field != fieldNumber {
			return nil
		}
		return unmarshalProtoSelector(value, out)
	})
}

func unmarshalProtoProcessConfig(payload []byte) (*connectProcessConfig, error) {
	cfg := &connectProcessConfig{Envs: map[string]string{}}
	err := walkProtoFields(payload, func(field int, wire int, value []byte, _ uint64) error {
		switch field {
		case 1:
			cfg.Cmd = string(value)
		case 2:
			cfg.Args = append(cfg.Args, string(value))
		case 3:
			key, val, err := unmarshalProtoMapEntry(value)
			if err != nil {
				return err
			}
			cfg.Envs[key] = val
		case 4:
			cwd := string(value)
			cfg.Cwd = &cwd
		}
		return nil
	})
	if len(cfg.Envs) == 0 {
		cfg.Envs = nil
	}
	return cfg, err
}

func unmarshalProtoMapEntry(payload []byte) (string, string, error) {
	var key string
	var val string
	err := walkProtoFields(payload, func(field int, wire int, value []byte, _ uint64) error {
		switch field {
		case 1:
			key = string(value)
		case 2:
			val = string(value)
		}
		return nil
	})
	return key, val, err
}

func unmarshalProtoPTY(payload []byte) (*connectPTY, error) {
	pty := &connectPTY{}
	err := walkProtoFields(payload, func(field int, wire int, value []byte, _ uint64) error {
		if field != 1 {
			return nil
		}
		size := &connectPTYSize{}
		if err := walkProtoFields(value, func(field int, wire int, _ []byte, u uint64) error {
			switch field {
			case 1:
				size.Cols = uint32(u)
			case 2:
				size.Rows = uint32(u)
			}
			return nil
		}); err != nil {
			return err
		}
		pty.Size = size
		return nil
	})
	return pty, err
}

func unmarshalProtoSelector(payload []byte, out *connectProcessSelector) error {
	return walkProtoFields(payload, func(field int, wire int, value []byte, u uint64) error {
		switch field {
		case 1:
			out.PID = uint32(u)
		case 2:
			out.Tag = string(value)
		}
		return nil
	})
}

func unmarshalProtoProcessInput(payload []byte) (*connectProcessInput, error) {
	input := &connectProcessInput{}
	err := walkProtoFields(payload, func(field int, wire int, value []byte, _ uint64) error {
		switch field {
		case 1:
			input.Stdin = base64.StdEncoding.EncodeToString(value)
		case 2:
			input.PTY = base64.StdEncoding.EncodeToString(value)
		}
		return nil
	})
	return input, err
}

func walkProtoFields(payload []byte, fn func(field int, wire int, value []byte, varint uint64) error) error {
	for len(payload) > 0 {
		key, n := binary.Uvarint(payload)
		if n <= 0 {
			return fmt.Errorf("invalid proto field key")
		}
		payload = payload[n:]
		field := int(key >> 3)
		wire := int(key & 0x7)
		switch wire {
		case 0:
			value, n := binary.Uvarint(payload)
			if n <= 0 {
				return fmt.Errorf("invalid proto varint")
			}
			payload = payload[n:]
			if err := fn(field, wire, nil, value); err != nil {
				return err
			}
		case 2:
			size, n := binary.Uvarint(payload)
			if n <= 0 {
				return fmt.Errorf("invalid proto length")
			}
			payload = payload[n:]
			if uint64(len(payload)) < size {
				return fmt.Errorf("proto length exceeds payload")
			}
			value := payload[:size]
			payload = payload[size:]
			if err := fn(field, wire, value, 0); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported proto wire type %d", wire)
		}
	}
	return nil
}

func protoStringField(field int, value string) []byte {
	return protoBytesField(field, []byte(value))
}

func protoBytesField(field int, value []byte) []byte {
	out := protoVarint(uint64(field<<3 | 2))
	out = append(out, protoVarint(uint64(len(value)))...)
	out = append(out, value...)
	return out
}

func protoVarintField(field int, value uint64) []byte {
	out := protoVarint(uint64(field << 3))
	out = append(out, protoVarint(value)...)
	return out
}

func protoVarint(value uint64) []byte {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], value)
	return append([]byte(nil), buf[:n]...)
}

func encodeZigZag32(value int32) uint32 {
	return uint32(value<<1) ^ uint32(value>>31)
}

func boolVarint(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func writeConnectUnary(w http.ResponseWriter, codec connectCodec, body any) {
	if codec == connectCodecProto {
		w.Header().Set("Content-Type", "application/proto")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(http.StatusOK)
	if codec == connectCodecProto {
		payload, err := marshalConnectProto(body)
		if err == nil {
			_, _ = w.Write(payload)
		}
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
