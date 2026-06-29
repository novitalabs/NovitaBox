package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultProcessPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func processEnv(extra []string) []string {
	env := append([]string(nil), os.Environ()...)
	env = append(env, extra...)
	if envValue(env, "PATH") == "" {
		env = append(env, "PATH="+defaultProcessPath)
	}
	return env
}

func resolveProcessPath(name string, env []string) (string, error) {
	if name == "" || strings.Contains(name, "/") {
		return name, nil
	}
	searchPath := envValue(env, "PATH")
	if searchPath == "" {
		searchPath = os.Getenv("PATH")
	}
	if searchPath == "" {
		searchPath = defaultProcessPath
	}
	for _, dir := range filepath.SplitList(searchPath) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", name)
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}
