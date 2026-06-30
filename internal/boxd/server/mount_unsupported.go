//go:build !linux

package server

import (
	"fmt"
	"os"
)

func ensureReadonlyMount(device string, mountPath string, targetPath string) error {
	if targetPath != "" {
		if _, err := os.Stat(targetPath); err == nil {
			return nil
		}
	}
	return fmt.Errorf("mount %s on %s is only supported on linux", device, mountPath)
}
