package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/net/websocket"
)

type startProcessRequest struct {
	Cmd  []string `json:"cmd"`
	Cwd  string   `json:"cwd,omitempty"`
	TTY  bool     `json:"tty,omitempty"`
	Rows uint16   `json:"rows,omitempty"`
	Cols uint16   `json:"cols,omitempty"`
}

type startProcessResponse struct {
	Process processInfo `json:"process"`
}

type processInfo struct {
	ID  string `json:"id"`
	PID int    `json:"pid,omitempty"`
}

func main() {
	var proxyAddr string
	var cwd string
	var attachStdin bool
	var tty bool

	cmd := &cobra.Command{
		Use:   "boxctl",
		Short: "NovitaBox command line client",
	}
	execCmd := &cobra.Command{
		Use:   "exec [-it] <sandbox_id> <cmd> [args...]",
		Short: "Execute a command in a sandbox",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(proxyAddr, args[0], args[1:], cwd, attachStdin || tty)
		},
	}
	execCmd.Flags().BoolVarP(&attachStdin, "interactive", "i", false, "attach stdin")
	execCmd.Flags().BoolVarP(&tty, "tty", "t", false, "allocate a terminal")
	execCmd.Flags().StringVar(&cwd, "cwd", "", "working directory inside the sandbox")
	cmd.PersistentFlags().StringVar(&proxyAddr, "proxy", "http://127.0.0.1:8082", "boxproxy base URL")
	cmd.AddCommand(execCmd)

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "boxctl: %v\n", err)
		os.Exit(1)
	}
}

func runExec(proxyAddr string, sandboxID string, command []string, cwd string, interactive bool) error {
	rows, cols := terminalSize()
	startReq := startProcessRequest{
		Cmd:  command,
		Cwd:  cwd,
		TTY:  true,
		Rows: rows,
		Cols: cols,
	}
	body, err := json.Marshal(startReq)
	if err != nil {
		return err
	}

	base := strings.TrimRight(proxyAddr, "/")
	startURL := fmt.Sprintf("%s/v1/sandboxes/%s/processes", base, url.PathEscape(sandboxID))
	resp, err := http.Post(startURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("start process: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("start process failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var startResp startProcessResponse
	if err := json.NewDecoder(resp.Body).Decode(&startResp); err != nil {
		return fmt.Errorf("decode start process response: %w", err)
	}
	if startResp.Process.ID == "" {
		return fmt.Errorf("start process response missing process id")
	}

	wsURL, err := processWebSocketURL(base, sandboxID, startResp.Process.ID)
	if err != nil {
		return err
	}
	origin := "http://127.0.0.1"
	conn, err := websocket.Dial(wsURL, "", origin)
	if err != nil {
		return fmt.Errorf("connect process: %w", err)
	}
	defer conn.Close()
	var startFrame []byte
	_ = websocket.Message.Receive(conn, &startFrame)

	var restore func() error
	if interactive {
		restore, err = makeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("set terminal raw mode: %w", err)
		}
		defer restore()
		go resizeLoop(base, sandboxID, startResp.Process.ID)
	}

	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(os.Stdout, conn)
		errCh <- err
	}()
	err = <-errCh
	if err != nil && !isClosed(err) {
		return err
	}
	return nil
}

func processWebSocketURL(base string, sandboxID string, processID string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	u.Path = fmt.Sprintf("/v1/sandboxes/%s/processes/%s/connect", url.PathEscape(sandboxID), url.PathEscape(processID))
	u.RawQuery = ""
	return u.String(), nil
}

func resizeLoop(base string, sandboxID string, processID string) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)
	for {
		<-ch
		rows, cols := terminalSize()
		payload, _ := json.Marshal(map[string]uint16{"rows": rows, "cols": cols})
		_, _ = http.Post(
			fmt.Sprintf("%s/v1/sandboxes/%s/processes/%s/resize", strings.TrimRight(base, "/"), url.PathEscape(sandboxID), url.PathEscape(processID)),
			"application/json",
			bytes.NewReader(payload),
		)
	}
}

func terminalSize() (uint16, uint16) {
	return getTerminalSize(int(os.Stdout.Fd()))
}

func isClosed(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "use of closed network connection") ||
		strings.Contains(text, "websocket: close") ||
		strings.Contains(text, "EOF") ||
		strings.Contains(text, "i/o timeout") ||
		strings.Contains(text, "broken pipe")
}
