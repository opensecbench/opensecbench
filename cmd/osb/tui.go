package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/opensecbench/opensecbench/pkg/client"
	"github.com/opensecbench/opensecbench/pkg/controlplane"
	"github.com/opensecbench/opensecbench/pkg/tui"
)

// runTUI launches the terminal client (ADR-0063). It attaches to a control plane if one is reachable at
// addr, and otherwise runs one in-process — the same plane the desktop embeds — so `osb tui` works
// standalone over SSH with no separate daemon to start.
func runTUI(ctx context.Context, addr string) error {
	redirectLogs() // keep control-plane logging off the rendered screen

	if c := client.New(addr, client.WithToken(resolveToken())); reachable(ctx, c) {
		return tui.Run(ctx, c)
	}
	// The user pointed us at a specific daemon that isn't answering; don't silently start a different
	// one — that would attach to the wrong state.
	if os.Getenv("OSB_API") != "" {
		return fmt.Errorf("no control plane reachable at %s (OSB_API is set); start it, or unset OSB_API to run one locally", addr)
	}

	cp, err := controlplane.Start(controlplane.Options{Addr: "127.0.0.1:0"})
	if err != nil {
		return fmt.Errorf("start control plane: %w", err)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cp.Shutdown(shutCtx)
	}()
	return tui.Run(ctx, client.New(cp.BaseURL, client.WithToken(cp.Token)))
}

// reachable reports whether a control plane answers a health check within a short window.
func reachable(ctx context.Context, c *client.Client) bool {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	_, err := c.Health(ctx)
	return err == nil
}

// redirectLogs sends the standard logger (which the in-process control plane writes to) to a file in
// the data dir, so its output never corrupts the full-screen TUI. Falls back to discarding.
func redirectLogs() {
	if p, err := controlplane.DefaultDBPath(); err == nil {
		if f, err := os.OpenFile(filepath.Join(filepath.Dir(p), "tui.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			log.SetOutput(f)
			return
		}
	}
	log.SetOutput(io.Discard)
}

// isTTY reports whether both stdin and stdout are terminals, so bare `osb` only auto-launches the
// interactive client when a human is actually at a terminal (piped/scripted use still prints usage).
func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fo, err := os.Stdout.Stat()
	return err == nil && fo.Mode()&os.ModeCharDevice != 0
}
