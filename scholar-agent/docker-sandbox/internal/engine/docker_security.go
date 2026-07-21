package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func secureDockerRunArgs(image string) ([]string, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return nil, fmt.Errorf("sandbox image is empty")
	}
	if !sandboxImageAllowed(image) {
		return nil, fmt.Errorf("sandbox image %q is not in SANDBOX_IMAGE_ALLOWLIST", image)
	}

	args := []string{
		"run", "-d", "--rm",
		"--cpus", envOrDefault("SANDBOX_CPU_LIMIT", "2"),
		"--memory", envOrDefault("SANDBOX_MEMORY_LIMIT", "4g"),
		"--pids-limit", envOrDefault("SANDBOX_PIDS_LIMIT", "256"),
		"--security-opt", "no-new-privileges:true",
		"--cap-drop", "ALL",
		"--network", envOrDefault("SANDBOX_NETWORK_MODE", "bridge"),
	}
	if user := strings.TrimSpace(os.Getenv("SANDBOX_CONTAINER_USER")); user != "" {
		args = append(args, "--user", user)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SANDBOX_READ_ONLY_ROOT")), "true") {
		args = append(args, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=512m")
	}
	return args, nil
}

func sandboxImageAllowed(image string) bool {
	raw := strings.TrimSpace(os.Getenv("SANDBOX_IMAGE_ALLOWLIST"))
	if raw == "" {
		return true
	}
	for _, prefix := range strings.Split(raw, ",") {
		prefix = strings.TrimSpace(prefix)
		if prefix != "" && strings.HasPrefix(image, prefix) {
			return true
		}
	}
	return false
}

func allowedSandboxMountRoots() []string {
	raw := strings.TrimSpace(os.Getenv("SANDBOX_WORKSPACE_ROOTS"))
	if raw == "" {
		raw = os.TempDir()
	}
	roots := make([]string, 0)
	for _, root := range filepath.SplitList(raw) {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if absolute, err := filepath.Abs(root); err == nil {
			absolute = filepath.Clean(absolute)
			if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
				absolute = filepath.Clean(resolved)
			}
			roots = append(roots, absolute)
		}
	}
	return roots
}

func mountPathAllowed(path string) bool {
	cleanPath := filepath.Clean(path)
	for _, root := range allowedSandboxMountRoots() {
		relative, err := filepath.Rel(root, cleanPath)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func normalizeAndAuthorizeMountPath(mountPath string) (string, error) {
	absPath, err := filepath.Abs(mountPath)
	if err != nil {
		return "", fmt.Errorf("resolve mount path failed: %w", err)
	}
	absPath = filepath.Clean(absPath)
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("inspect mount path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("mount path %q is not a directory", absPath)
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve mount path symlinks: %w", err)
	}
	absPath = filepath.Clean(resolvedPath)
	if !mountPathAllowed(absPath) {
		return "", fmt.Errorf("mount path %q is outside SANDBOX_WORKSPACE_ROOTS", absPath)
	}
	if runtime.GOOS == "windows" {
		absPath = strings.ReplaceAll(absPath, "\\", "/")
	}
	return absPath, nil
}
