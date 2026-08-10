package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
)

// DefaultImage is the minimal sandbox image a terminal opens into when no image is configured — it ships
// only a POSIX shell. For a real assessment toolchain (git, python, curl/jq, cloud CLIs), build the image
// in pkg/session/Dockerfile and set OSB_SESSION_IMAGE to it (see the controlplane wiring). Kept minimal by
// default so a terminal works out of the box without pulling a large image.
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

// Image reports the default image sessions open into.
func (m *Manager) Image() string { return m.image }

// Available reports whether the local Docker daemon is reachable.
func Available() bool {
	return exec.Command("docker", "info").Run() == nil
}

// OpenOpts overrides per-session defaults.
type OpenOpts struct {
	Image   string // empty = manager default
	Network string // empty = "none"
	Memory  string // empty = "512m"
	CPUs    string // empty = "1"
	Env     map[string]string
}

// EffectiveImage returns the image that will be used.
func (m *Manager) EffectiveImage(o OpenOpts) string {
	if o.Image != "" {
		return o.Image
	}
	return m.image
}

// Open starts a sandboxed container with default settings.
func (m *Manager) Open(ctx context.Context, container string) (*Handle, error) {
	return m.OpenWith(ctx, container, OpenOpts{})
}

// OpenWith starts a sandboxed container with the given options and attaches a shell over a PTY.
func (m *Manager) OpenWith(ctx context.Context, container string, opts OpenOpts) (*Handle, error) {
	img := m.EffectiveImage(opts)
	network := opts.Network
	if network == "" {
		network = "none"
	}
	memory := opts.Memory
	if memory == "" {
		memory = "512m"
	}
	cpus := opts.CPUs
	if cpus == "" {
		cpus = "1"
	}

	runArgs := []string{
		"run", "-d", "--rm",
		"--name", container,
		"--network", network,
		"--memory", memory,
		"--cpus", cpus,
	}
	for k, v := range opts.Env {
		runArgs = append(runArgs, "-e", k+"="+v)
	}
	runArgs = append(runArgs, img, "sleep", "infinity")

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
