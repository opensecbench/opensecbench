// Package browser locates a local Chromium-based browser binary, shared by the proxy's
// preconfigured-browser launch and the report engine's headless PDF rendering.
package browser

import (
	"errors"
	"fmt"
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

// ProxyArgs builds the Chromium flags for a throwaway browser pointed at the intercepting proxy and
// trusting only its CA (via --ignore-certificate-errors-spki-list) — no system trust change.
func ProxyArgs(profileDir string, port int, spki, url string) []string {
	if url == "" {
		url = "about:blank"
	}
	return []string{
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		fmt.Sprintf("--proxy-server=127.0.0.1:%d", port),
		"--proxy-bypass-list=<-loopback>", // route loopback through the proxy too
		"--ignore-certificate-errors-spki-list=" + spki,
		url,
	}
}

// Launch starts a preconfigured throwaway browser detached (non-blocking) and cleans up its temp
// profile after it exits. Used by the desktop "Open browser" binding.
func Launch(port int, spki, url string) error {
	bin, err := Resolve("")
	if err != nil {
		return err
	}
	profile, err := os.MkdirTemp("", "osb-browser-")
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, ProxyArgs(profile, port, spki, url)...)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profile)
		return err
	}
	go func() {
		_ = cmd.Wait()
		_ = os.RemoveAll(profile)
	}()
	return nil
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
