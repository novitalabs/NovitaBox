//go:build linux

package server

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func startShellProcess(shellPath string, cols uint16, rows uint16) (*exec.Cmd, *os.File, error) {
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	return startPTYProcess(shellPath, nil, "", nil, cols, rows)
}

func startPTYProcess(cmdPath string, args []string, cwd string, env []string, cols uint16, rows uint16) (*exec.Cmd, *os.File, error) {
	if cmdPath == "" {
		cmdPath = "/bin/sh"
	}
	ptm, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open pty master: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = ptm.Close()
		}
	}()

	if err := unix.IoctlSetPointerInt(int(ptm.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		return nil, nil, fmt.Errorf("unlock pty: %w", err)
	}
	ptyNum, err := unix.IoctlGetInt(int(ptm.Fd()), unix.TIOCGPTN)
	if err != nil {
		return nil, nil, fmt.Errorf("get pty number: %w", err)
	}
	ptsPath := fmt.Sprintf("/dev/pts/%d", ptyNum)
	pts, err := os.OpenFile(ptsPath, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open pty slave %s: %w", ptsPath, err)
	}
	defer pts.Close()

	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	_ = unix.IoctlSetWinsize(int(ptm.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})

	cmd := exec.Command(cmdPath, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = pts
	cmd.Stdout = pts
	cmd.Stderr = pts
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start shell: %w", err)
	}

	cleanup = false
	return cmd, ptm, nil
}

func resizePTY(terminal processTerminal, cols uint16, rows uint16) error {
	if terminal == nil {
		return fmt.Errorf("pty is not assigned")
	}
	file, ok := terminal.(*os.File)
	if !ok {
		return fmt.Errorf("pty terminal has unsupported type")
	}
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	return unix.IoctlSetWinsize(int(file.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
}
