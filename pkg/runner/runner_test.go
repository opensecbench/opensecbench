package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireDocker skips the test when Docker is not available (e.g. restricted CI).
func requireDocker(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("docker not available")
	}
}

func TestLocalRunnerReadOnlyMountAndStdout(t *testing.T) {
	requireDocker(t)

	dir := t.TempDir()
	const content = "cross-tenant write succeeded"
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := LocalRunner{}.Run(context.Background(), RunSpec{
		Image:    "alpine:3",
		Cmd:      []string{"cat", "/in/data.txt"},
		Mounts:   []Mount{{Source: dir, Target: "/in", ReadOnly: true}},
		Timeout:  90 * time.Second,
		MemoryMB: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %s", res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(string(res.Stdout)) != content {
		t.Fatalf("stdout = %q, want %q", res.Stdout, content)
	}
}

func TestLocalRunnerNonZeroExitIsNotAnError(t *testing.T) {
	requireDocker(t)

	res, err := LocalRunner{}.Run(context.Background(), RunSpec{
		Image:   "alpine:3",
		Cmd:     []string{"sh", "-c", "exit 3"},
		Timeout: 90 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", res.ExitCode)
	}
}
