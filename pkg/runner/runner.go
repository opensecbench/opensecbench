// Package runner executes capabilities in isolated sandboxes (ADR-0004). The interface is
// transport-agnostic so a remote runner can be added later behind the same contract; P2 ships a
// local Docker runner.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Mount binds a host path into the container.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// RunSpec is a fully-specified sandboxed execution.
type RunSpec struct {
	Image string
	Cmd   []string
	Env   []string
	// SecretEnv holds sensitive env vars (name→value) injected without exposing values on the
	// command line: the value is placed in the docker CLI's environment and passed through by name
	// (`-e NAME`), so it never appears in `ps` output (ADR-0011). Transient; never persisted.
	SecretEnv map[string]string
	// Name, if set, names the container so it can be stopped with `docker kill <name>`.
	Name string
	// Stdin, if set, is fed to the container's standard input (docker run -i). Used by one-shot
	// commands that read their input on stdin rather than argv.
	Stdin    []byte
	Mounts   []Mount
	Network  string // default "none"
	Workdir  string
	Timeout  time.Duration
	MemoryMB int
	CPUs     float64
}

// Result is the outcome of a run. A non-zero ExitCode is a normal result, not an error; err is
// non-nil only when the run could not be carried out (e.g. the runtime is missing or timed out).
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
}

// Runner executes a RunSpec.
type Runner interface {
	Run(ctx context.Context, spec RunSpec) (Result, error)
	Name() string
}

// LocalRunner runs each capability in an ephemeral Docker container with sandboxing defaults:
// no network, read-only source mounts, and resource/time limits.
type LocalRunner struct{}

// Name identifies this runner in provenance records.
func (LocalRunner) Name() string { return "local-docker" }

// Available reports whether the Docker CLI is on PATH.
func Available() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// Run executes spec via `docker run` and captures its output.
func (LocalRunner) Run(ctx context.Context, spec RunSpec) (Result, error) {
	if spec.Image == "" {
		return Result{}, errors.New("runner: image required")
	}

	network := spec.Network
	if network == "" {
		network = "none"
	}
	args := []string{"run", "--rm", "--network", network}
	// Baseline hardening (ADR-0004): block setuid privilege gain and bound process count against a fork
	// bomb. Both are safe for every scanner image and independent of the mounted content's permissions.
	// (Dropping capabilities or running non-root also helps but changes what files the scanner can read,
	// so it needs per-image validation — see TODO.)
	args = append(args, "--security-opt=no-new-privileges", "--pids-limit=1024")
	if len(spec.Stdin) > 0 {
		args = append(args, "-i") // attach stdin so the container can read spec.Stdin
	}
	if spec.Name != "" {
		args = append(args, "--name", spec.Name)
	}
	if spec.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", spec.MemoryMB))
	}
	if spec.CPUs > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%g", spec.CPUs))
	}
	for _, m := range spec.Mounts {
		mode := "rw"
		if m.ReadOnly {
			mode = "ro"
		}
		args = append(args, "-v", fmt.Sprintf("%s:%s:%s", m.Source, m.Target, mode))
	}
	if spec.Workdir != "" {
		args = append(args, "-w", spec.Workdir)
	}
	for _, e := range spec.Env {
		args = append(args, "-e", e)
	}
	// Secrets: pass by name only so the value stays out of the process argv; the value rides in the
	// docker CLI's own environment (see cmd.Env below).
	var secretEnv []string
	for k, v := range spec.SecretEnv {
		args = append(args, "-e", k)
		secretEnv = append(secretEnv, k+"="+v)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Cmd...)

	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	if len(secretEnv) > 0 {
		cmd.Env = append(os.Environ(), secretEnv...)
	}
	if len(spec.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Duration: time.Since(start)}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil // non-zero exit is a normal result
		}
		if ctx.Err() == context.DeadlineExceeded {
			return res, fmt.Errorf("runner: timed out after %s", spec.Timeout)
		}
		return res, fmt.Errorf("runner: %w", err)
	}
	return res, nil
}
