package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
)

// DefaultImage is the sandbox image a terminal opens into. It ships a POSIX shell; richer
// toolchain images (with the aws/az CLIs, per the plan) become configurable later.
//
// TODO(P7+): configurable per-project session image; bake the assessment toolchain + aws/az CLIs
// into an OSB base image pinned by digest (mirrors the runner base-image plan).
const DefaultImage = "alpine:3"

// Manager opens terminal sessions as shells inside sandboxed containers.
type Manager struct {
	image string
}

// NewManager returns a Manager using the given image (empty uses DefaultImage).
func NewManager(image string) *Manager {
	if image == "" {
		image = DefaultImage
	}
	return &Manager{image: image}
}

// Image reports the image sessions open into.
func (m *Manager) Image() string { return m.image }

// Available reports whether the local Docker daemon is reachable.
func Available() bool {
	return exec.Command("docker", "info").Run() == nil
}

// Open starts a sandboxed container named `container` and attaches a shell to it over a PTY. The
// container has no network by default (a local sandbox); scoped network access comes later.
func (m *Manager) Open(ctx context.Context, container string) (*Handle, error) {
	runArgs := []string{
		"run", "-d", "--rm",
		"--name", container,
		"--network", "none",
		"--memory", "512m",
		"--cpus", "1",
		m.image, "sleep", "infinity",
	}
	if out, err := exec.CommandContext(ctx, "docker", runArgs...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("start session container: %w: %s", err, strings.TrimSpace(string(out)))
	}
	stop := func() { _ = exec.Command("docker", "kill", container).Run() } // --rm removes it

	cmd := exec.Command("docker", "exec", "-it", container, "/bin/sh")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		stop()
		return nil, fmt.Errorf("attach shell: %w", err)
	}
	return newHandle(container, &osPTY{f: ptmx, cmd: cmd}, stop), nil
}

// osPTY adapts a creack/pty master file + its command to the PTY interface.
type osPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func (o *osPTY) Read(p []byte) (int, error)  { return o.f.Read(p) }
func (o *osPTY) Write(p []byte) (int, error) { return o.f.Write(p) }

func (o *osPTY) Resize(rows, cols uint16) error {
	return pty.Setsize(o.f, &pty.Winsize{Rows: rows, Cols: cols})
}

func (o *osPTY) Close() error {
	err := o.f.Close()
	if o.cmd.Process != nil {
		_ = o.cmd.Process.Kill()
		_, _ = o.cmd.Process.Wait()
	}
	return err
}
