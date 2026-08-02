//go:build desktop

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/opensecbench/opensecbench/pkg/browser"
)

// App holds the Wails context and exposes OS-native operations to the frontend. Per ADR-0001,
// Wails bindings are used only for OS-native concerns (here, file/directory pickers) — never for
// domain logic, which goes through the control-plane HTTP API.
type App struct {
	ctx     context.Context
	baseURL string // control-plane base URL, for server-side downloads that keep the token off the wire
	token   string // local API bearer token (ADR-0061), handed to the webview via APIToken
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// APIToken returns the local API bearer token (ADR-0061). The sandboxed webview can't read the
// on-disk token file, so it fetches the token over this bridge once at boot and attaches it to every
// control-plane request. This is the one exception to "Wails bindings are OS-native only": handing
// the webview its credential is a bootstrap concern, not domain logic.
func (a *App) APIToken() string { return a.token }

// APIBase returns the control-plane base URL for the webview (fetched at boot alongside APIToken). It
// lets the desktop window attach to an external daemon on a non-default address instead of the embedded
// one — see main.go's OSB_API attach mode.
func (a *App) APIBase() string { return a.baseURL }

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

// SaveArtifact downloads a control-plane resource to a user-chosen file (ADR-0061). Go holds the API
// token, so the fetch is authenticated with an Authorization header and the bytes never transit a
// URL or the system browser — the desktop replacement for opening a download link. path must be an
// API-relative path (e.g. "/v1/proxy/ca"); it returns the saved path, or "" if the user cancels.
func (a *App) SaveArtifact(path, suggestedName string) (string, error) {
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("path must be API-relative")
	}
	dest, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: suggestedName,
		Title:           "Save file",
	})
	if err != nil || dest == "" {
		return "", err
	}

	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, a.baseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return dest, nil
}
