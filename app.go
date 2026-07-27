//go:build desktop

package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/opensecbench/opensecbench/pkg/browser"
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

// OpenURL opens a URL in the user's default system browser. The native WebKit webview does not honour
// target="_blank"/window.open (there is no tab strip), so links that must render a page — generated
// reports, transcripts, downloads served by the local API — go through here instead of an <a target>.
func (a *App) OpenURL(url string) error {
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

// OpenProxyBrowser launches a throwaway Chromium preconfigured to use the project's intercepting
// proxy (port) and trust only its CA (spki) — the desktop equivalent of `osb proxy browser`. This
// is an OS-native action (launching a local process), which is why it's a Wails binding.
func (a *App) OpenProxyBrowser(port int, spki string) error {
	return browser.Launch(port, spki, "about:blank")
}
