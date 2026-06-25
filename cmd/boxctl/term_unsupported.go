//go:build !linux && !darwin

package main

import "fmt"

func getTerminalSize(fd int) (uint16, uint16) {
	return 24, 80
}

func makeRaw(fd int) (func() error, error) {
	return nil, fmt.Errorf("raw terminal mode is not supported on this platform")
}
