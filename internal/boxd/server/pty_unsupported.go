//go:build !linux

package server

import (
	"fmt"
	"io"
	"os/exec"
)

type processPipe struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func startPTYProcess(cmdPath string, args []string, cwd string, env []string, cols uint16, rows uint16) (*exec.Cmd, *processPipe, error) {
	if cmdPath == "" {
		cmdPath = "/bin/sh"
	}
	cmdEnv := processEnv(append(env, "TERM=xterm-256color"))
	resolvedPath, err := resolveProcessPath(cmdPath, cmdEnv)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(resolvedPath, args...)
	cmd.Env = cmdEnv
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("open shell stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("open shell stdout: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("start shell: %w", err)
	}
	return cmd, &processPipe{stdin: stdin, stdout: stdout}, nil
}

func resizePTY(terminal processTerminal, cols uint16, rows uint16) error {
	if terminal == nil {
		return fmt.Errorf("pty is not assigned")
	}
	return nil
}

func (p *processPipe) Read(data []byte) (int, error) {
	return p.stdout.Read(data)
}

func (p *processPipe) Write(data []byte) (int, error) {
	return p.stdin.Write(data)
}

func (p *processPipe) Close() error {
	_ = p.stdin.Close()
	return p.stdout.Close()
}
