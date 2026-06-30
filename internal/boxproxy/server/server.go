package server

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"github.com/novitalabs/NovitaBox/internal/wsutil"
	"golang.org/x/net/websocket"
)

const sandboxIDHeader = "Novita-Sandbox-Id"

type Server struct {
	cfg        config.Config
	logger     *log.Logger
	store      store.Store
	httpServer *http.Server
}

func New(cfg config.Config, logger *log.Logger, st store.Store) *Server {
	s := &Server{cfg: cfg, logger: logger, store: st}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/", s.handleSandbox)
	s.httpServer = &http.Server{
		Addr:              cfg.BoxProxy.Addr,
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
	s.logger.Info("starting boxproxy", "addr", s.cfg.BoxProxy.Addr)
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

	s.logger.Info("stopped boxproxy")
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","service":"boxproxy"}` + "\n"))
}

func (s *Server) handleSandbox(w http.ResponseWriter, r *http.Request) {
	sandboxID := sandboxIDFromRequest(r)
	rest := strings.TrimPrefix(r.URL.Path, "/")
	if sandboxID == "" || rest == "" {
		http.NotFound(w, r)
		return
	}
	if isProcessConnectPath(rest) {
		websocket.Server{
			Handler:   websocket.Handler(s.handleSandboxWebSocket),
			Handshake: acceptWebSocket,
		}.ServeHTTP(w, r)
		return
	}
	if rest == "processes" || strings.HasPrefix(rest, "processes/") {
		s.proxySandboxHTTP(w, r, sandboxID, rest)
		return
	}
	if strings.HasPrefix(rest, "process.Process/") {
		s.proxySandboxHTTP(w, r, sandboxID, rest)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleSandboxWebSocket(client *websocket.Conn) {
	sandboxID := sandboxIDFromRequest(client.Request())
	rest := strings.TrimPrefix(client.Request().URL.Path, "/")
	if sandboxID == "" || rest == "" {
		_ = websocket.Message.Send(client, []byte("unsupported sandbox websocket route"))
		wsutil.CloseWebSocket(client)
		return
	}
	s.proxySandboxWebSocket(client, sandboxID, rest)
}

func (s *Server) proxySandboxWebSocket(client *websocket.Conn, sandboxID string, rest string) {
	targetURL, err := s.boxdWebSocketURL(sandboxID, rest, client.Request().URL.Query())
	if err != nil {
		s.logger.Error("resolve sandbox websocket target failed", "sandbox_id", sandboxID, "path", rest, "error", err)
		_ = websocket.Message.Send(client, []byte(err.Error()))
		wsutil.CloseWebSocket(client)
		return
	}

	origin := "http://" + client.Request().Host
	guest, err := websocket.Dial(targetURL, "", origin)
	if err != nil {
		s.logger.Error("connect sandbox websocket failed", "sandbox_id", sandboxID, "target", targetURL, "error", err)
		_ = websocket.Message.Send(client, []byte(fmt.Sprintf("connect sandbox websocket failed: %v", err)))
		wsutil.CloseWebSocket(client)
		return
	}
	defer wsutil.CloseWebSocket(guest)
	defer wsutil.CloseWebSocket(client)

	s.logger.Info("proxying sandbox websocket", "sandbox_id", sandboxID, "target", targetURL)
	errCh := make(chan error, 2)
	go func() {
		errCh <- wsutil.CopyWebSocket(guest, client)
	}()
	go func() {
		errCh <- wsutil.CopyWebSocket(client, guest)
	}()
	if err := <-errCh; err != nil {
		s.logger.Warn("sandbox websocket stream closed with error", "sandbox_id", sandboxID, "error", err)
	}
}

func (s *Server) proxySandboxHTTP(w http.ResponseWriter, r *http.Request, sandboxID string, rest string) {
	target, err := s.boxdHTTPURL(sandboxID, rest, r.URL.RawQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Error("proxy sandbox http failed", "sandbox_id", sandboxID, "target", target, "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	copyResponseBody(w, resp.Body)
}

func copyResponseBody(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func (s *Server) boxdWebSocketURL(sandboxID string, rest string, query url.Values) (string, error) {
	path, err := boxdPathFromSandboxRest(rest)
	if err != nil {
		return "", err
	}
	host, err := s.boxdHost(sandboxID)
	if err != nil {
		return "", err
	}
	out := url.URL{
		Scheme:   "ws",
		Host:     host,
		Path:     path,
		RawQuery: query.Encode(),
	}
	return out.String(), nil
}

func (s *Server) boxdHTTPURL(sandboxID string, rest string, rawQuery string) (string, error) {
	path, err := boxdPathFromSandboxRest(rest)
	if err != nil {
		return "", err
	}
	host, err := s.boxdHost(sandboxID)
	if err != nil {
		return "", err
	}
	out := url.URL{
		Scheme:   "http",
		Host:     host,
		Path:     path,
		RawQuery: rawQuery,
	}
	return out.String(), nil
}

func (s *Server) boxdHost(sandboxID string) (string, error) {
	hostIP, err := s.hostAccessIP(sandboxID)
	if err != nil {
		return "", err
	}
	_, port, err := net.SplitHostPort(s.cfg.Boxd.Addr)
	if err != nil {
		return "", fmt.Errorf("parse boxd address %q: %w", s.cfg.Boxd.Addr, err)
	}
	return net.JoinHostPort(hostIP, port), nil
}

func (s *Server) hostAccessIP(sandboxID string) (string, error) {
	if s.store == nil {
		return "", errors.New("sandbox store is not configured")
	}
	record, err := s.store.GetSandbox(context.Background(), sandboxID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", fmt.Errorf("sandbox %q not found", sandboxID)
		}
		return "", err
	}
	if record.NetworkSlot == 0 {
		return "", fmt.Errorf("sandbox %q has no active network slot", sandboxID)
	}
	return indexedIP(s.cfg.Network.HostAccessCIDR, record.NetworkSlot)
}

func sandboxIDFromRequest(r *http.Request) string {
	if sandboxID := r.Header.Get(sandboxIDHeader); sandboxID != "" {
		return sandboxID
	}
	if sandboxID := r.URL.Query().Get("sandboxID"); sandboxID != "" {
		return sandboxID
	}
	return sandboxIDFromHost(r.Host)
}

func sandboxIDFromHost(host string) string {
	hostname := host
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		hostname = parsedHost
	}
	firstLabel := strings.SplitN(hostname, ".", 2)[0]
	parts := strings.SplitN(firstLabel, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[1]
}

func boxdPathFromSandboxRest(rest string) (string, error) {
	if rest == "processes" {
		return "/processes", nil
	}
	if strings.HasPrefix(rest, "processes/") {
		return "/" + rest, nil
	}
	if strings.HasPrefix(rest, "process.Process/") {
		return "/" + rest, nil
	}
	return "", fmt.Errorf("unsupported sandbox route %q", rest)
}

func isProcessConnectPath(rest string) bool {
	if !strings.HasPrefix(rest, "processes/") {
		return false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	return len(parts) == 3 && parts[0] == "processes" && parts[1] != "" && parts[2] == "connect"
}

func indexedIP(cidr string, index uint32) (string, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("parse cidr %q: %w", cidr, err)
	}
	ip4 := network.IP.To4()
	if ip4 == nil {
		return "", fmt.Errorf("cidr %q must be IPv4", cidr)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("cidr %q must be IPv4", cidr)
	}
	totalIPs := uint32(1) << uint(32-ones)
	if index == 0 || index >= totalIPs {
		return "", fmt.Errorf("ip index %d is outside cidr %q", index, cidr)
	}
	base := binary.BigEndian.Uint32(ip4)
	out := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(out, base+index)
	return out.String(), nil
}
