//go:build desktop

package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App holds the Wails context and exposes OS-native operations to the frontend. Per ADR-0001,
// Wails bindings are used only for OS-native concerns (here, file/directory pickers) — never for
// domain logic, which goes through the control-plane HTTP API.
type App struct {
	ctx context.Context
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// SelectDirectory opens a native directory picker and returns the chosen path ("" if cancelled).
func (a *App) SelectDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Select a source directory"})
}

// SelectFile opens a native file picker and returns the chosen path ("" if cancelled).
func (a *App) SelectFile() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "Select a file"})
}
