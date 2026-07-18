//go:build desktop

// Command opensecbench is the desktop application. It boots the control plane in-process and
// renders the React frontend via Wails (ADR-0001: the frontend is a thin client that calls the
// local HTTP API; Wails only provides the window/webview).
//
// Build/run with Wails, which sets the `desktop` build tag automatically:
//
//	wails dev      # live-reload development
//	wails build    # package a desktop binary
//
// Plain `go build ./...` skips this file (see main_nondesktop.go) so CI does not need the
// webkit/gtk desktop toolchain.
package main

import (
	"context"
	"embed"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/opensecbench/opensecbench/pkg/controlplane"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// The frontend defaults to the control plane on 127.0.0.1:7373 (see frontend/src/api.ts),
	// so we boot it there in-process.
	cp, err := controlplane.Start(controlplane.Options{Addr: "127.0.0.1:7373"})
	if err != nil {
		panic(err)
	}

	err = wails.Run(&options.App{
		Title:            "OpenSecBench",
		Width:            1280,
		Height:           820,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: &options.RGBA{R: 11, G: 14, B: 20, A: 1},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnShutdown: func(context.Context) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = cp.Shutdown(ctx)
		},
	})
	if err != nil {
		panic(err)
	}
}
