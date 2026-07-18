// Package browser locates a local Chromium-based browser binary, shared by the proxy's
// preconfigured-browser launch and the report engine's headless PDF rendering.
package browser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ErrNotFound is returned when no Chromium-based browser can be located.
var ErrNotFound = errors.New("browser: no Chromium-based browser found (set OSB_BROWSER)")

// Resolve returns a browser binary path: an explicit override, then the OSB_BROWSER env var, then
// platform defaults.
func Resolve(override string) (string, error) {
	if override != "" {
		if p, err := exec.LookPath(override); err == nil {
			return p, nil
		}
		return "", errors.New("browser: " + override + " not found in PATH")
	}
	if env := os.Getenv("OSB_BROWSER"); env != "" {
		if p, err := exec.LookPath(env); err == nil {
			return p, nil
		}
	}
	for _, cand := range Candidates() {
		if filepath.IsAbs(cand) {
			if _, err := os.Stat(cand); err == nil {
				return cand, nil
			}
			continue
		}
		if p, err := exec.LookPath(cand); err == nil {
			return p, nil
		}
	}
	return "", ErrNotFound
}

// Candidates lists the browser binaries tried, most-preferred first, for the current platform.
func Candidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	case "windows":
		return []string{"chrome.exe", "msedge.exe", "brave.exe"}
	default: // linux and other unixes
		return []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"brave-browser", "microsoft-edge", "microsoft-edge-stable",
		}
	}
}
