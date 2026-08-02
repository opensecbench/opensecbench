//go:build desktop

// Command opensecbench is the desktop application. It boots the control plane in-process and
// renders the React frontend via Wails (ADR-0001: the frontend is a thin client that calls the
// local HTTP API; Wails only provides the window/webview).
//
// Build/run with Wails, passing the `desktop` build tag (or use the Makefile targets `make dev`
// / `make build`):
//
//	wails dev -tags desktop      # live-reload development
//	wails build -tags desktop    # package a desktop binary
//
// Plain `go build ./...` skips this file (see main_nondesktop.go) so CI does not need the
// webkit/gtk desktop toolchain. Wails does NOT add the tag automatically — it must be passed.
package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/opensecbench/opensecbench/pkg/controlplane"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Two modes (ADR-0001): by default the desktop app embeds the control plane in-process so it is
	// self-contained; setting OSB_API attaches the window to an already-running daemon instead (e.g.
	// `make daemon`), so one shared backend can serve several clients — a future CLI/TUI, or two
	// windows. The frontend fetches the base URL + token over the Wails bridge at boot (APIBase/APIToken).
	app, shutdown, err := boot()
	if err != nil {
		log.Fatal(err)
	}

	if err := wails.Run(&options.App{
		Title:            "OpenSecBench",
		Width:            1280,
		Height:           820,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: &options.RGBA{R: 11, G: 14, B: 20, A: 1},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnShutdown: func(context.Context) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			shutdown(ctx)
		},
		Bind: []interface{}{app},
	}); err != nil {
		panic(err)
	}
}

// boot resolves the App's control-plane connection: an external daemon (OSB_API) or an embedded one.
// It returns the App plus a shutdown func the window calls on close (a no-op when attaching, since we
// don't own the external daemon's lifecycle).
func boot() (*App, func(context.Context), error) {
	if external := strings.TrimSpace(os.Getenv("OSB_API")); external != "" {
		// Attach to an already-running control plane. Prefer an explicit OSB_API_TOKEN; otherwise read the
		// daemon's token file from the default data dir (same host, so the 0600 file is readable — ADR-0061).
		token := strings.TrimSpace(os.Getenv("OSB_API_TOKEN"))
		if token == "" {
			if p, derr := controlplane.DefaultDBPath(); derr == nil {
				token, _ = controlplane.ReadAPIToken(filepath.Dir(p))
			}
		}
		if token == "" {
			log.Printf("OSB_API=%s: no API token found — set OSB_API_TOKEN or ensure the daemon's api-token file is readable; requests may be rejected", external)
		}
		log.Printf("attaching to external control plane at %s", external)
		return &App{baseURL: external, token: token}, func(context.Context) {}, nil
	}

	// The frontend defaults to the control plane on 127.0.0.1:7373 (see frontend/src/api.ts).
	cp, err := controlplane.Start(controlplane.Options{Addr: "127.0.0.1:7373"})
	if err != nil {
		return nil, nil, fmt.Errorf("could not start the control plane on 127.0.0.1:7373: %w\n"+
			"Is another OpenSecBench daemon or desktop instance already running? "+
			"Attach to it with OSB_API=http://127.0.0.1:7373, or free the port "+
			"(e.g. `lsof -ti tcp:7373 | xargs -r kill`) and retry.", err)
	}
	return &App{baseURL: cp.BaseURL, token: cp.Token}, func(ctx context.Context) { _ = cp.Shutdown(ctx) }, nil
}
