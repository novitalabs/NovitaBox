package runtime

import (
	"path/filepath"
	"strings"
)

func ContainerPathInRootfs(rootfs string, guestPath string) string {
	return filepath.Join(rootfs, filepath.FromSlash(strings.TrimPrefix(guestPath, "/")))
}
