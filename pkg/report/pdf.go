package report

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/opensecbench/opensecbench/pkg/browser"
)

// FormatPDF is produced by printing the HTML render through a headless browser.
const FormatPDF Format = "pdf"

// ErrNoBrowser indicates PDF rendering is unavailable because no Chromium browser was found.
var ErrNoBrowser = errors.New("report: PDF rendering needs a Chromium browser (set OSB_BROWSER)")

// HTMLToPDF renders an HTML document to PDF using a headless Chromium. It writes the HTML to a temp
// file and drives the browser's --print-to-pdf; the whole thing runs offline. Returns ErrNoBrowser
// if no browser is installed, so callers can degrade to MD/HTML.
func HTMLToPDF(ctx context.Context, htmlDoc []byte) ([]byte, error) {
	bin, err := browser.Resolve("")
	if err != nil {
		return nil, ErrNoBrowser
	}

	dir, err := os.MkdirTemp("", "osb-pdf-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	inPath := filepath.Join(dir, "report.html")
	outPath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(inPath, htmlDoc, 0o600); err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin,
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--no-pdf-header-footer",
		"--print-to-pdf="+outPath,
		"file://"+inPath,
	)
	// A dedicated profile dir avoids colliding with the user's browser.
	cmd.Env = append(os.Environ(), "HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("headless print failed: %w: %s", err, tail(out, 300))
	}

	pdf, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read rendered pdf: %w", err)
	}
	return pdf, nil
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
