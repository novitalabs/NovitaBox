//go:build linux

package server

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func ensureReadonlyMount(device string, mountPath string, targetPath string) error {
	if targetPath != "" {
		if _, err := os.Stat(targetPath); err == nil {
			return nil
		}
	}
	if err := os.MkdirAll(mountPath, 0o755); err != nil {
		return fmt.Errorf("create mount path %q: %w", mountPath, err)
	}
	if err := unix.Mount(device, mountPath, "ext4", unix.MS_RDONLY, ""); err != nil {
		if targetPath != "" {
			if _, statErr := os.Stat(targetPath); statErr == nil {
				return nil
			}
		}
		return fmt.Errorf("mount %s on %s: %w", device, mountPath, err)
	}
	return nil
}
