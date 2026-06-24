//go:build !linux

package server

import "errors"

func reflinkFile(string, string) error {
	return errors.New("reflink is not supported on this platform")
}
