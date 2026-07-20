package sandboxserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecureDockerRunArgsApplyResourceAndPrivilegeLimits(t *testing.T) {
	t.Setenv("SANDBOX_CPU_LIMIT", "1.5")
	t.Setenv("SANDBOX_MEMORY_LIMIT", "2g")
	t.Setenv("SANDBOX_PIDS_LIMIT", "128")
	t.Setenv("SANDBOX_NETWORK_MODE", "none")
	t.Setenv("SANDBOX_CONTAINER_USER", "65534:65534")
	t.Setenv("SANDBOX_READ_ONLY_ROOT", "true")

	args, err := secureDockerRunArgs("python:3.11-slim")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"--cpus 1.5", "--memory 2g", "--pids-limit 128",
		"--security-opt no-new-privileges:true", "--cap-drop ALL",
		"--network none", "--user 65534:65534", "--read-only",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected %q in docker args: %s", expected, joined)
		}
	}
}

func TestSandboxImageAndMountAllowlist(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:3.11")
	if _, err := secureDockerRunArgs("ubuntu:latest"); err == nil {
		t.Fatal("expected image outside allowlist to be rejected")
	}

	root := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOTS", root)
	inside := filepath.Join(root, "run-1")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeDockerMountPath(inside); err != nil {
		t.Fatalf("expected workspace path to be accepted: %v", err)
	}
	if _, err := normalizeDockerMountPath(filepath.Dir(root)); err == nil {
		t.Fatal("expected parent directory mount to be rejected")
	}
	outside := t.TempDir()
	symlink := filepath.Join(root, "linked-outside")
	if err := os.Symlink(outside, symlink); err == nil {
		if _, err := normalizeDockerMountPath(symlink); err == nil {
			t.Fatal("expected symlink escaping workspace root to be rejected")
		}
	}
}
